export async function getServerSideProps() {
  const goApiUrl = process.env.GO_API_URL || 'http://localhost:8080'
  let notes = []
  let error = null

  try {
    const res = await fetch(`${goApiUrl}/api/notes`)
    notes = await res.json()
  } catch (err) {
    error = err.message
  }

  return {
    props: {
      notes,
      error,
      fetchedAt: new Date().toISOString(),
      goApiUrl,
    },
  }
}

export default function Home({ notes, error, fetchedAt, goApiUrl }) {
  return (
    <div style={{ maxWidth: 600, margin: '40px auto', fontFamily: 'system-ui' }}>
      <h1>DeBros Notes (SSR)</h1>
      <p style={{ color: '#666', fontSize: 14 }}>
        Next.js SSR + Go API + SQLite
      </p>
      <p style={{ color: '#888', fontSize: 12 }}>
        Server-side fetched at: {fetchedAt} from {goApiUrl}
      </p>

      {error && <p style={{ color: 'red' }}>Error: {error}</p>}

      {notes.length === 0 ? (
        <p>No notes yet. Add some via the Go API or React app.</p>
      ) : (
        notes.map((n) => (
          <div
            key={n.id}
            style={{
              border: '1px solid #ddd',
              padding: 12,
              marginBottom: 8,
              borderRadius: 4,
            }}
          >
            <strong>{n.title}</strong>
            <p>{n.content}</p>
            <small style={{ color: '#999' }}>{n.created_at}</small>
          </div>
        ))
      )}

      <p style={{ marginTop: 20, color: '#aaa', fontSize: 12 }}>
        This page is server-side rendered on every request.
        Refresh to see new notes added from other apps.
      </p>
    </div>
  )
}
