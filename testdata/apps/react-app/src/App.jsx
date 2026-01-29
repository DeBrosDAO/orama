import React, { useState, useEffect } from 'react'

const API_URL = import.meta.env.VITE_API_URL || 'http://localhost:3000'

export default function App() {
  const [notes, setNotes] = useState([])
  const [title, setTitle] = useState('')
  const [content, setContent] = useState('')
  const [meta, setMeta] = useState(null)
  const [error, setError] = useState(null)

  async function fetchNotes() {
    try {
      const res = await fetch(`${API_URL}/api/notes`)
      const data = await res.json()
      setNotes(data.notes || [])
      setMeta({ fetched_at: data.fetched_at, source: data.source })
      setError(null)
    } catch (err) {
      setError(err.message)
    }
  }

  useEffect(() => { fetchNotes() }, [])

  async function addNote(e) {
    e.preventDefault()
    if (!title.trim()) return
    await fetch(`${API_URL}/api/notes`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ title, content }),
    })
    setTitle('')
    setContent('')
    fetchNotes()
  }

  return (
    <div style={{ maxWidth: 600, margin: '40px auto', fontFamily: 'system-ui' }}>
      <h1>DeBros Notes</h1>
      <p style={{ color: '#666', fontSize: 14 }}>
        React Static + Node.js Proxy + Go API + SQLite
      </p>
      {meta && (
        <p style={{ color: '#888', fontSize: 12 }}>
          Source: {meta.source} | Fetched: {meta.fetched_at}
        </p>
      )}
      {error && <p style={{ color: 'red' }}>Error: {error}</p>}

      <form onSubmit={addNote} style={{ marginBottom: 20 }}>
        <input
          value={title}
          onChange={e => setTitle(e.target.value)}
          placeholder="Title"
          style={{ display: 'block', width: '100%', padding: 8, marginBottom: 8 }}
        />
        <textarea
          value={content}
          onChange={e => setContent(e.target.value)}
          placeholder="Content"
          style={{ display: 'block', width: '100%', padding: 8, marginBottom: 8 }}
        />
        <button type="submit" style={{ padding: '8px 16px' }}>Add Note</button>
        <button type="button" onClick={fetchNotes} style={{ padding: '8px 16px', marginLeft: 8 }}>
          Refresh
        </button>
      </form>

      {notes.length === 0 ? (
        <p>No notes yet.</p>
      ) : (
        notes.map(n => (
          <div key={n.id} style={{ border: '1px solid #ddd', padding: 12, marginBottom: 8, borderRadius: 4 }}>
            <strong>{n.title}</strong>
            <p>{n.content}</p>
            <small style={{ color: '#999' }}>{n.created_at}</small>
          </div>
        ))
      )}
    </div>
  )
}
