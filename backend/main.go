// main.go
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

var (
	db *gorm.DB
	hub *Hub
)

type User struct {
	ID      uint    `json:"id" gorm:"primaryKey"`
	Name    string  `json:"name"`
	Balance float64 `json:"balance"`
}

type TransactionLog struct {
	ID            string      `json:"id"`
	Type          string      `json:"type"`
	Query         string      `json:"query,omitempty"`
	Result        interface{} `json:"result,omitempty"`
	IsolationLevel string     `json:"isolation_level,omitempty"`
	Timestamp     int64       `json:"timestamp"`
	LockInfo      *LockInfo   `json:"lock_info,omitempty"`
}

type LockInfo struct {
	Type    string `json:"type"`    // "shared", "exclusive", "gap"
	Table   string `json:"table"`
	RowID   *uint  `json:"row_id,omitempty"`
}

type Hub struct {
	clients    map[*Client]bool
	broadcast  chan []byte
	register   chan *Client
	unregister chan *Client
}

type Client struct {
	hub  *Hub
	conn *websocket.Conn
	send chan []byte
}

func main() {
	// データベース接続
	dsn := "appuser:apppassword@tcp(mysql:3306)/transaction_learning?charset=utf8mb4&parseTime=True&loc=Local"
	var err error
	db, err = gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatal("データベース接続エラー:", err)
	}

	// マイグレーション
	db.AutoMigrate(&User{})

	// WebSocketハブ初期化
	hub = &Hub{
		clients:    make(map[*Client]bool),
		broadcast:  make(chan []byte),
		register:   make(chan *Client),
		unregister: make(chan *Client),
	}
	go hub.run()

	// ルータ設定
	r := gin.Default()
	r.Use(cors.Default())

	// API ルート
	api := r.Group("/api")
	{
		api.GET("/users", getUsers)
		api.POST("/scenarios/dirty-read", dirtyReadScenario)
		api.GET("/isolation-level", getIsolationLevel)
		api.POST("/isolation-level", setIsolationLevel)
	}

	// WebSocket エンドポイント
	r.GET("/ws", handleWebSocket)

	r.Run(":8080")
}

// WebSocket ハンドラ
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func handleWebSocket(c *gin.Context) {
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("WebSocket升级失败: %v", err)
		return
	}
	
	client := &Client{
		hub:  hub,
		conn: conn,
		send: make(chan []byte, 256),
	}
	
	client.hub.register <- client
	
	go client.writePump()
	go client.readPump()
}

// Dirty Read シナリオ実装
func dirtyReadScenario(c *gin.Context) {
	var request struct {
		UserID uint    `json:"user_id"`
		Amount float64 `json:"amount"`
	}
	
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	go executeDirtyReadScenario(request.UserID, request.Amount)
	
	c.JSON(http.StatusOK, gin.H{"message": "Dirty Read シナリオを開始しました"})
}

func executeDirtyReadScenario(userID uint, amount float64) {
	// トランザクション1: 未コミットの更新
	go func() {
		tx1 := db.Begin()
		
		// 分離レベルをREAD UNCOMMITTEDに設定
		tx1.Exec("SET SESSION TRANSACTION ISOLATION LEVEL READ UNCOMMITTED")
		
		broadcastLog(TransactionLog{
			ID:             "tx1",
			Type:           "transaction_started",
			IsolationLevel: "READ_UNCOMMITTED",
			Timestamp:      getCurrentTimestamp(),
		})
		
		// 残高を取得
		var user User
		tx1.First(&user, userID)
		
		broadcastLog(TransactionLog{
			ID:        "tx1",
			Type:      "query_executed",
			Query:     "SELECT * FROM users WHERE id = ?",
			Result:    user,
			Timestamp: getCurrentTimestamp(),
		})
		
		// 残高を更新（まだコミットしない）
		newBalance := user.Balance + amount
		tx1.Model(&user).Update("balance", newBalance)
		
		broadcastLog(TransactionLog{
			ID:        "tx1",
			Type:      "query_executed",
			Query:     "UPDATE users SET balance = ? WHERE id = ?",
			Result:    map[string]interface{}{"new_balance": newBalance},
			Timestamp: getCurrentTimestamp(),
		})
		
		// 3秒待機（Dirty Readが発生する時間を作る）
		time.Sleep(3 * time.Second)
		
		// ロールバック
		tx1.Rollback()
		
		broadcastLog(TransactionLog{
			ID:        "tx1",
			Type:      "transaction_rollbacked",
			Timestamp: getCurrentTimestamp(),
		})
	}()
	
	// トランザクション2: Dirty Readを実行
	go func() {
		time.Sleep(1 * time.Second) // tx1の更新後に実行
		
		tx2 := db.Begin()
		tx2.Exec("SET SESSION TRANSACTION ISOLATION LEVEL READ UNCOMMITTED")
		
		broadcastLog(TransactionLog{
			ID:             "tx2",
			Type:           "transaction_started",
			IsolationLevel: "READ_UNCOMMITTED",
			Timestamp:      getCurrentTimestamp(),
		})
		
		// 未コミットのデータを読み取る（Dirty Read）
		var user User
		tx2.First(&user, userID)
		
		broadcastLog(TransactionLog{
			ID:        "tx2",
			Type:      "query_executed",
			Query:     "SELECT * FROM users WHERE id = ? (Dirty Read!)",
			Result:    user,
			Timestamp: getCurrentTimestamp(),
		})
		
		tx2.Commit()
		
		broadcastLog(TransactionLog{
			ID:        "tx2",
			Type:      "transaction_committed",
			Timestamp: getCurrentTimestamp(),
		})
	}()
}

// ヘルパー関数
func broadcastLog(log TransactionLog) {
	data, _ := json.Marshal(log)
	hub.broadcast <- data
}

func getCurrentTimestamp() int64 {
	return time.Now().UnixMilli()
}

func getUsers(c *gin.Context) {
	var users []User
	db.Find(&users)
	c.JSON(http.StatusOK, users)
}

func getIsolationLevel(c *gin.Context) {
	var level string
	db.Raw("SELECT @@transaction_isolation").Scan(&level)
	c.JSON(http.StatusOK, gin.H{"isolation_level": level})
}

func setIsolationLevel(c *gin.Context) {
	var request struct {
		Level string `json:"level"`
	}
	
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	
	db.Exec("SET SESSION TRANSACTION ISOLATION LEVEL " + request.Level)
	c.JSON(http.StatusOK, gin.H{"message": "分離レベルを変更しました"})
}

// WebSocket Hub実装
func (h *Hub) run() {
	for {
		select {
		case client := <-h.register:
			h.clients[client] = true
			
		case client := <-h.unregister:
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
			}
			
		case message := <-h.broadcast:
			for client := range h.clients {
				select {
				case client.send <- message:
				default:
					close(client.send)
					delete(h.clients, client)
				}
			}
		}
	}
}

func (c *Client) readPump() {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close()
	}()
	
	for {
		_, _, err := c.conn.ReadMessage()
		if err != nil {
			break
		}
	}
}

func (c *Client) writePump() {
	defer c.conn.Close()
	
	for {
		select {
		case message, ok := <-c.send:
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			
			c.conn.WriteMessage(websocket.TextMessage, message)
		}
	}
}