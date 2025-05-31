// App.tsx
import React, { useState, useEffect } from 'react';
import './App.css';

interface User {
  id: number;
  name: string;
  balance: number;
}

interface TransactionLog {
  id: string;
  type: string;
  query?: string;
  result?: any;
  isolation_level?: string;
  timestamp: number;
  lock_info?: LockInfo;
}

interface LockInfo {
  type: string;
  table: string;
  row_id?: number;
}

interface TransactionState {
  id: string;
  status: 'active' | 'committed' | 'rollbacked';
  isolation_level: string;
  logs: TransactionLog[];
}

const App: React.FC = () => {
  const [users, setUsers] = useState<User[]>([]);
  const [transactions, setTransactions] = useState<Map<string, TransactionState>>(new Map());
  const [logs, setLogs] = useState<TransactionLog[]>([]);
  const [ws, setWs] = useState<WebSocket | null>(null);
  const [isolationLevel, setIsolationLevel] = useState<string>('REPEATABLE-READ');

  useEffect(() => {
    // WebSocket接続
    const websocket = new WebSocket('ws://localhost:8080/ws');
    
    websocket.onopen = () => {
      console.log('WebSocket接続完了');
      setWs(websocket);
    };
    
    websocket.onmessage = (event) => {
      const log: TransactionLog = JSON.parse(event.data);
      handleTransactionLog(log);
    };
    
    websocket.onclose = () => {
      console.log('WebSocket切断');
    };

    // 初期データ取得
    fetchUsers();
    fetchIsolationLevel();

    return () => {
      websocket.close();
    };
  }, []);

  const fetchUsers = async () => {
    try {
      const response = await fetch('http://localhost:8080/api/users');
      const data = await response.json();
      setUsers(data);
    } catch (error) {
      console.error('ユーザー取得エラー:', error);
    }
  };

  const fetchIsolationLevel = async () => {
    try {
      const response = await fetch('http://localhost:8080/api/isolation-level');
      const data = await response.json();
      setIsolationLevel(data.isolation_level);
    } catch (error) {
      console.error('分離レベル取得エラー:', error);
    }
  };

  const handleTransactionLog = (log: TransactionLog) => {
    setLogs(prev => [...prev, log]);
    
    setTransactions(prev => {
      const newTransactions = new Map(prev);
      
      if (log.type === 'transaction_started') {
        newTransactions.set(log.id, {
          id: log.id,
          status: 'active',
          isolation_level: log.isolation_level || '',
          logs: [log]
        });
      } else {
        const existing = newTransactions.get(log.id);
        if (existing) {
          existing.logs.push(log);
          
          if (log.type === 'transaction_committed') {
            existing.status = 'committed';
          } else if (log.type === 'transaction_rollbacked') {
            existing.status = 'rollbacked';
          }
        }
      }
      
      return newTransactions;
    });

    // ユーザーデータの更新が含まれている場合は再取得
    if (log.type === 'query_executed' && log.query?.includes('UPDATE')) {
      setTimeout(fetchUsers, 100);
    }
  };

  const executeDirtyRead = async () => {
    try {
      await fetch('http://localhost:8080/api/scenarios/dirty-read', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({
          user_id: 1,
          amount: 500
        }),
      });
    } catch (error) {
      console.error('Dirty Readシナリオ実行エラー:', error);
    }
  };

  const changeIsolationLevel = async (level: string) => {
    try {
      await fetch('http://localhost:8080/api/isolation-level', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({ level }),
      });
      setIsolationLevel(level);
    } catch (error) {
      console.error('分離レベル変更エラー:', error);
    }
  };

  const clearLogs = () => {
    setLogs([]);
    setTransactions(new Map());
  };

  return (
    <div className="app">
      <header className="app-header">
        <h1>MySQL トランザクション学習システム</h1>
        <div className="isolation-level-selector">
          <label>分離レベル: </label>
          <select 
            value={isolationLevel} 
            onChange={(e) => changeIsolationLevel(e.target.value)}
          >
            <option value="READ-UNCOMMITTED">READ UNCOMMITTED</option>
            <option value="READ-COMMITTED">READ COMMITTED</option>
            <option value="REPEATABLE-READ">REPEATABLE READ</option>
            <option value="SERIALIZABLE">SERIALIZABLE</option>
          </select>
        </div>
      </header>

      <main className="main-content">
        <div className="left-panel">
          <div className="scenario-buttons">
            <h3>シナリオ実行</h3>
            <button onClick={executeDirtyRead} className="scenario-btn dirty-read">
              Dirty Read 実行
            </button>
            <button disabled className="scenario-btn">
              Non-repeatable Read (未実装)
            </button>
            <button disabled className="scenario-btn">
              Phantom Read (未実装)
            </button>
            <button disabled className="scenario-btn">
              デッドロック (未実装)
            </button>
          </div>

          <div className="users-table">
            <h3>ユーザーテーブル</h3>
            <table>
              <thead>
                <tr>
                  <th>ID</th>
                  <th>名前</th>
                  <th>残高</th>
                </tr>
              </thead>
              <tbody>
                {users.map(user => (
                  <tr key={user.id} className="user-row">
                    <td>{user.id}</td>
                    <td>{user.name}</td>
                    <td>¥{user.balance.toLocaleString()}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>

        <div className="right-panel">
          <div className="transactions-panel">
            <div className="panel-header">
              <h3>アクティブなトランザクション</h3>
              <button onClick={clearLogs} className="clear-btn">
                ログクリア
              </button>
            </div>
            
            <div className="transactions-list">
              {Array.from(transactions.values()).map(tx => (
                <div key={tx.id} className={`transaction-card ${tx.status}`}>
                  <div className="transaction-header">
                    <span className="tx-id">{tx.id}</span>
                    <span className={`tx-status ${tx.status}`}>
                      {tx.status === 'active' ? '実行中' : 
                       tx.status === 'committed' ? 'コミット済み' : 'ロールバック済み'}
                    </span>
                  </div>
                  <div className="tx-isolation">
                    分離レベル: {tx.isolation_level}
                  </div>
                </div>
              ))}
            </div>
          </div>

          <div className="logs-panel">
            <h3>トランザクションログ</h3>
            <div className="logs-container">
              {logs.map((log, index) => (
                <div key={index} className={`log-entry ${log.type}`}>
                  <div className="log-header">
                    <span className="log-tx-id">{log.id}</span>
                    <span className="log-timestamp">
                      {new Date(log.timestamp).toLocaleTimeString()}
                    </span>
                  </div>
                  <div className="log-content">
                    {log.type === 'transaction_started' && (
                      <span className="log-message">
                        🚀 トランザクション開始 ({log.isolation_level})
                      </span>
                    )}
                    {log.type === 'query_executed' && (
                      <div>
                        <div className="log-query">📝 {log.query}</div>
                        {log.result && (
                          <div className="log-result">
                            💾 結果: {JSON.stringify(log.result, null, 2)}
                          </div>
                        )}
                      </div>
                    )}
                    {log.type === 'transaction_committed' && (
                      <span className="log-message">✅ トランザクションコミット</span>
                    )}
                    {log.type === 'transaction_rollbacked' && (
                      <span className="log-message">❌ トランザクションロールバック</span>
                    )}
                  </div>
                </div>
              ))}
            </div>
          </div>
        </div>
      </main>
    </div>
  );
};

export default App;