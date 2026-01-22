import { useState } from 'react'
import './App.css'

function App() {
  const [count, setCount] = useState(0)

  return (
    <>
      <div className="app-container">
        <h1>Orama Network Test App</h1>
        <p className="test-marker" data-testid="app-title">
          E2E Testing - React Vite Static Deployment
        </p>
        <div className="card">
          <button onClick={() => setCount((count) => count + 1)}>
            count is {count}
          </button>
          <p>
            This is a test application for validating static site deployments.
          </p>
        </div>
        <p className="version">Version: 1.0.0</p>
      </div>
    </>
  )
}

export default App
