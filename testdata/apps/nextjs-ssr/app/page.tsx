export default function Home() {
  return (
    <main className="container">
      <h1>Orama Network Next.js Test</h1>
      <p className="test-marker" data-testid="app-title">
        E2E Testing - Next.js SSR Deployment
      </p>
      <div className="card">
        <h2>Server-Side Rendering Test</h2>
        <p>This page is rendered on the server.</p>
        <p>Current time: {new Date().toISOString()}</p>
      </div>
      <div className="api-test">
        <h3>API Routes:</h3>
        <ul>
          <li><a href="/api/hello">/api/hello</a> - Simple greeting endpoint</li>
          <li><a href="/api/data">/api/data</a> - JSON data endpoint</li>
        </ul>
      </div>
      <p className="version">Version: 1.0.0</p>
    </main>
  )
}
