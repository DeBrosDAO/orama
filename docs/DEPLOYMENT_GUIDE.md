# Orama Network Deployment Guide

Complete guide for deploying applications and managing databases on Orama Network.

## Table of Contents

- [Overview](#overview)
- [Authentication](#authentication)
- [Deploying Static Sites (React, Vue, etc.)](#deploying-static-sites)
- [Deploying Next.js Applications](#deploying-nextjs-applications)
- [Deploying Go Backends](#deploying-go-backends)
- [Deploying Node.js Backends](#deploying-nodejs-backends)
- [Managing SQLite Databases](#managing-sqlite-databases)
- [How Domains Work](#how-domains-work)
- [Full-Stack Application Example](#full-stack-application-example)
- [Managing Deployments](#managing-deployments)
- [Troubleshooting](#troubleshooting)

---

## Overview

Orama Network provides a decentralized platform for deploying web applications and managing databases. Each deployment:

- **Gets a unique domain** automatically (e.g., `myapp.orama.network`)
- **Isolated per namespace** - your data and apps are completely separate from others
- **Served from IPFS** (static) or **runs as a process** (dynamic apps)
- **Fully managed** - automatic health checks, restarts, and logging

### Supported Deployment Types

| Type | Description | Use Case | Domain Example |
|------|-------------|----------|----------------|
| **Static** | HTML/CSS/JS files served from IPFS | React, Vue, Angular, plain HTML | `myapp.orama.network` |
| **Next.js** | Next.js with SSR support | Full-stack Next.js apps | `myapp.orama.network` |
| **Go** | Compiled Go binaries | REST APIs, microservices | `api.orama.network` |
| **Node.js** | Node.js applications | Express APIs, TypeScript backends | `backend.orama.network` |

---

## Authentication

Before deploying, authenticate with your wallet:

```bash
# Authenticate
orama auth login

# Check authentication status
orama auth whoami
```

Your API key is stored securely and used for all deployment operations.

---

## Deploying Static Sites

Deploy static sites built with React, Vue, Angular, or any static site generator.

### React/Vite Example

```bash
# 1. Build your React app
cd my-react-app
npm run build

# 2. Deploy the build directory
orama deploy static ./dist --name my-react-app

# Output:
# 📦 Creating tarball from ./dist...
# ☁️  Uploading to Orama Network...
#
# ✅ Deployment successful!
#
# Name:         my-react-app
# Type:         static
# Status:       active
# Version:      1
# Content CID:  QmXxxx...
#
# URLs:
#   • https://my-react-app.orama.network
```

### What Happens Behind the Scenes

1. **Tarball Creation**: CLI automatically creates a `.tar.gz` from your directory
2. **IPFS Upload**: Files are uploaded to IPFS and pinned across the network
3. **DNS Record**: A DNS record is created pointing `my-react-app.orama.network` to the gateway
4. **Instant Serving**: Your app is immediately accessible via the URL

### Features

- ✅ **SPA Routing**: Unknown routes automatically serve `/index.html` (perfect for React Router)
- ✅ **Correct Content-Types**: Automatically detects and serves `.html`, `.css`, `.js`, `.json`, `.png`, etc.
- ✅ **Caching**: `Cache-Control: public, max-age=3600` headers for optimal performance
- ✅ **Zero Downtime Updates**: Use `--update` flag to update without downtime

### Updating a Deployment

```bash
# Make changes to your app
# Rebuild
npm run build

# Update deployment
orama deploy static ./dist --name my-react-app --update

# Version increments automatically (1 → 2)
```

---

## Deploying Next.js Applications

Deploy Next.js apps with full SSR (Server-Side Rendering) support.

### Prerequisites

> ⚠️ **IMPORTANT**: Your `next.config.js` MUST have `output: 'standalone'` for SSR deployments.

```js
// next.config.js
/** @type {import('next').NextConfig} */
const nextConfig = {
  output: 'standalone',  // REQUIRED for SSR deployments
}

module.exports = nextConfig
```

This setting makes Next.js create a standalone build in `.next/standalone/` that can run without `node_modules`.

### Next.js with SSR

```bash
# 1. Ensure next.config.js has output: 'standalone'

# 2. Deploy the project source directory with SSR enabled
#    (the CLI installs dependencies, runs the build, and uploads for you)
cd my-nextjs-app
orama deploy nextjs . --name my-nextjs --ssr

# Output:
# 📦 Installing dependencies...
# 🔨 Building Next.js application...
# 📦 Creating tarball from standalone output...
# ☁️  Uploading to Orama Network...
#
# ✅ Deployment successful!
#
# Name:         my-nextjs
# Type:         nextjs
# Status:       active
# Version:      1
# Port:         10100
#
# URLs:
#   • https://my-nextjs.orama.network
#
# ⚠️  Note: SSR deployment may take a minute to start. Check status with: orama app get my-nextjs
```

### What Happens Behind the Scenes

1. **Build**: The CLI runs `npm install` (if `node_modules` is missing) and `npm run build` in your project directory
2. **Tarball Upload**: The `.next/standalone/` output (with `.next/static` and `public` copied in) is tarballed and uploaded
3. **Home Node Assignment**: A node is chosen to host your app based on capacity
4. **Port Allocation**: A unique port (10100-19999) is assigned
5. **Systemd Service**: A systemd service is created to run `node server.js`
6. **Health Checks**: Gateway monitors your app every 30 seconds
7. **Reverse Proxy**: Gateway proxies requests from your domain to the local port

### Static Next.js Export (No SSR)

If you export Next.js to static HTML:

```bash
# next.config.js
module.exports = {
  output: 'export'
}

# Build and deploy as static
npm run build
orama deploy static ./out --name my-nextjs-static
```

---

## Deploying Go Backends

Deploy Go backends for high-performance APIs.

### Prerequisites

> ⚠️ **IMPORTANT**: Your Go application MUST:
> 1. Have a `go.mod` in the project root (the CLI cross-compiles for you)
> 2. Listen on the port from `PORT` environment variable
> 3. Implement a `/health` endpoint that returns HTTP 200 when ready
> 4. Build with `CGO_ENABLED=0` (the CLI compiles with cgo disabled — use pure-Go dependencies)

### Go REST API Example

```bash
# Deploy the project source directory — the CLI cross-compiles
# for linux/amd64 (CGO_ENABLED=0) and uploads for you
cd my-go-api
orama deploy go . --name my-api

# Output:
# 🔨 Building Go binary (linux/amd64)...
# 📦 Creating tarball...
# ☁️  Uploading to Orama Network...
#
# ✅ Deployment successful!
#
# Name:         my-api
# Type:         go
# Status:       active
# Version:      1
# Port:         10101
#
# URLs:
#   • https://my-api.orama.network
```

### Example Go API Code

```go
// main.go
package main

import (
    "encoding/json"
    "log"
    "net/http"
    "os"
)

func main() {
    port := os.Getenv("PORT")
    if port == "" {
        port = "8080"
    }

    http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
        json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
    })

    http.HandleFunc("/api/users", func(w http.ResponseWriter, r *http.Request) {
        users := []map[string]interface{}{
            {"id": 1, "name": "Alice"},
            {"id": 2, "name": "Bob"},
        }
        json.NewEncoder(w).Encode(users)
    })

    log.Printf("Starting server on port %s", port)
    log.Fatal(http.ListenAndServe(":"+port, nil))
}
```

### Important Notes

- **Environment Variables**: The `PORT` environment variable is automatically set to your allocated port
- **Health Endpoint**: **REQUIRED** - Must implement `/health` that returns HTTP 200 when ready
- **Automatic Cross-Compilation**: The CLI runs `go build -o app .` with `GOOS=linux GOARCH=amd64 CGO_ENABLED=0` — no manual build needed
- **No cgo**: Because builds use `CGO_ENABLED=0`, dependencies requiring cgo (e.g. `mattn/go-sqlite3`) will not work — use pure-Go alternatives (e.g. `modernc.org/sqlite`)
- **Systemd Managed**: Runs as a systemd service with auto-restart on failure
- **Port Range**: Allocated ports are in the range 10100-19999

---

## Deploying Node.js Backends

Deploy Node.js/Express/TypeScript backends.

### Prerequisites

> ⚠️ **IMPORTANT**: Your Node.js application MUST:
> 1. Listen on the port from `PORT` environment variable
> 2. Implement a `/health` endpoint that returns HTTP 200 when ready
> 3. Have a valid `package.json` with either:
>    - A `start` script (runs via `npm start`), OR
>    - A `main` field pointing to entry file (runs via `node {main}`), OR
>    - An `index.js` file (default fallback)

### Express API Example

```bash
# Deploy the project source directory — the CLI installs dependencies,
# runs the build script (if any), and uploads for you
cd my-node-api
orama deploy nodejs . --name my-node-api

# Output:
# 📦 Installing dependencies...
# 🔨 Building...
# 📦 Creating tarball...
# ☁️  Uploading to Orama Network...
#
# ✅ Deployment successful!
#
# Name:         my-node-api
# Type:         nodejs
# Status:       active
# Version:      1
# Port:         10102
#
# URLs:
#   • https://my-node-api.orama.network
```

### Example Node.js API

```javascript
// server.js
const express = require('express');
const app = express();
const port = process.env.PORT || 8080;

app.get('/health', (req, res) => {
  res.json({ status: 'healthy' });
});

app.get('/api/data', (req, res) => {
  res.json({ message: 'Hello from Orama Network!' });
});

app.listen(port, () => {
  console.log(`Server running on port ${port}`);
});
```

### Important Notes

- **Environment Variables**: The `PORT` environment variable is automatically set to your allocated port
- **Health Endpoint**: **REQUIRED** - Must implement `/health` that returns HTTP 200 when ready
- **Dependencies**: The CLI runs `npm install --production` locally if `node_modules` is missing; `node_modules` and hidden files are excluded from the uploaded tarball, and dependencies are installed on the server
- **Start Command Detection**:
  1. If `package.json` has `scripts.start` → runs `npm start`
  2. Else if `package.json` has `main` field → runs `node {main}`
  3. Else → runs `node index.js`
- **Systemd Managed**: Runs as a systemd service with auto-restart on failure

---

## Managing SQLite Databases

Each namespace gets its own isolated SQLite databases.

### Creating a Database

```bash
# Create a new database
orama db create my-database

# Output:
# ✅ Database created successfully!
#
# Name:      my-database
# Home Node: node-abc123
# Created:   2024-01-22T10:30:00Z
```

The database file is stored on the home node at `/opt/orama/.orama/data/sqlite/{your-namespace}/my-database.db`.

### Executing Queries

```bash
# Create a table
orama db query my-database "CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT, email TEXT)"

# Insert data
orama db query my-database "INSERT INTO users (name, email) VALUES ('Alice', 'alice@example.com')"

# Query data
orama db query my-database "SELECT * FROM users"

# Output:
# 📊 Query Result
# Rows: 1
#
# id              | name            | email
# ----------------+-----------------+-------------------------
# 1               | Alice           | alice@example.com
```

### Listing Databases

```bash
orama db list

# Output:
# NAME              SIZE        BACKUP CID      CREATED
# my-database       12.3 KB     QmYxxx...       2024-01-22 10:30
# prod-database     1.2 MB      -               2024-01-20 09:15
#
# Total: 2
```

### Backing Up to IPFS

```bash
# Create a backup
orama db backup my-database

# Output:
# ✅ Backup created
# CID: QmYxxx...
# Size: 12.3 KB

# List backups
orama db backups my-database

# Output:
# CID               SIZE        BACKED UP
# QmYxxx...         12.3 KB     2024-01-22 10:45
# QmZxxx...         15.1 KB     2024-01-22 14:20
```

### Database Features

- ✅ **WAL Mode**: Write-Ahead Logging for better concurrency
- ✅ **Namespace Isolation**: Complete separation between namespaces
- ✅ **On-Demand Backups**: Back up to IPFS anytime with `orama db backup`
- ✅ **ACID Transactions**: Full SQLite transactional support
- ✅ **Concurrent Reads**: Multiple readers can query simultaneously

---

## How Domains Work

### Domain Assignment

When you deploy an application, it automatically gets a domain:

```
Format: {deployment-name}.orama.network
Example: my-react-app.orama.network
```

### Node-Specific Domains (Optional)

For direct access to a specific node:

```
Format: {deployment-name}.node-{shortID}.orama.network
Example: my-react-app.node-LL1Qvu.orama.network
```

The `shortID` is derived from the node's peer ID (characters 9-14 of the full peer ID).
For example: `12D3KooWLL1QvumH...` → `LL1Qvu`

### DNS Resolution Flow

1. **Client**: Browser requests `my-react-app.orama.network`
2. **DNS**: CoreDNS server queries RQLite for DNS record
3. **Record**: Returns IP address of a gateway node (round-robin across all nodes)
4. **Gateway**: Receives request with `Host: my-react-app.orama.network` header
5. **Routing**: Domain routing middleware looks up deployment by domain
6. **Cross-Node Proxy**: If deployment is on a different node, request is forwarded
7. **Response**:
   - **Static**: Serves content from IPFS
   - **Dynamic**: Reverse proxies to the app's local port

### Cross-Node Routing

DNS uses round-robin, so requests may hit any node in the cluster. If a deployment is hosted on a different node than the one receiving the request, the gateway automatically proxies the request to the correct home node.

```
┌─────────────────────────────────────────────────────────────────┐
│                    Request Flow Example                          │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  Client                                                          │
│    │                                                             │
│    ▼                                                             │
│  DNS (round-robin) ───► Node-2 (141.227.165.154)                │
│                            │                                     │
│                            ▼                                     │
│                    Check: Is deployment here?                    │
│                            │                                     │
│                    No ─────┴───► Cross-node proxy                │
│                                       │                          │
│                                       ▼                          │
│                              Node-1 (141.227.165.168)            │
│                              (Home node for deployment)          │
│                                       │                          │
│                                       ▼                          │
│                              localhost:10100                     │
│                              (Deployment process)                │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

This is **transparent to users** - your app works regardless of which node handles the initial request.

### Custom Domains

The gateway supports attaching custom domains (e.g., `www.myapp.com`) to a deployment via HTTP API (no CLI subcommand yet):

- `POST /v1/deployments/domains/add` — registers the domain and returns a verification token
- `POST /v1/deployments/domains/verify` — checks for a TXT record at `_orama-verify.{domain}` matching the token
- `GET /v1/deployments/domains/list` — lists domains for a deployment
- `POST /v1/deployments/domains/remove` — detaches a domain

After verification, point your domain's A record to your deployment's node IP.

---

## Full-Stack Application Example

Deploy a complete full-stack application with React frontend, Go backend, and SQLite database.

### Architecture

```
┌─────────────────────────────────────────────┐
│   React Frontend (Static)                   │
│   Domain: myapp.orama.network               │
│   Deployed to IPFS                          │
└─────────────────┬───────────────────────────┘
                  │
                  │ API Calls
                  ▼
┌─────────────────────────────────────────────┐
│   Go Backend (Dynamic)                      │
│   Domain: myapp-api.orama.network           │
│   Port: 10100                               │
│   Systemd Service                           │
└─────────────────┬───────────────────────────┘
                  │
                  │ SQL Queries
                  ▼
┌─────────────────────────────────────────────┐
│   SQLite Database                           │
│   Name: myapp-db                            │
│   File: .../data/sqlite/ns/myapp-db.db      │
└─────────────────────────────────────────────┘
```

### Step 1: Create the Database

```bash
# Create database
orama db create myapp-db

# Create schema
orama db query myapp-db "CREATE TABLE users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    email TEXT UNIQUE NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
)"

# Insert test data
orama db query myapp-db "INSERT INTO users (name, email) VALUES ('Alice', 'alice@example.com')"
```

### Step 2: Deploy Go Backend

**Backend Code** (`main.go`):

```go
package main

import (
    "database/sql"
    "encoding/json"
    "log"
    "net/http"
    "os"

    _ "modernc.org/sqlite" // pure-Go driver (deployments build with CGO_ENABLED=0)
)

type User struct {
    ID        int    `json:"id"`
    Name      string `json:"name"`
    Email     string `json:"email"`
    CreatedAt string `json:"created_at"`
}

var db *sql.DB

func main() {
    // Orama only injects the PORT env var — the database path is up to you.
    // Databases created with `orama db create` live on the home node at:
    //   /opt/orama/.orama/data/sqlite/{your-namespace}/{db-name}.db
    dbPath := "/opt/orama/.orama/data/sqlite/your-namespace/myapp-db.db"

    var err error
    db, err = sql.Open("sqlite", dbPath)
    if err != nil {
        log.Fatal(err)
    }
    defer db.Close()

    port := os.Getenv("PORT")
    if port == "" {
        port = "8080"
    }

    // CORS middleware
    http.HandleFunc("/", corsMiddleware(routes))

    log.Printf("Starting server on port %s", port)
    log.Fatal(http.ListenAndServe(":"+port, nil))
}

func routes(w http.ResponseWriter, r *http.Request) {
    switch r.URL.Path {
    case "/health":
        json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
    case "/api/users":
        if r.Method == "GET" {
            getUsers(w, r)
        } else if r.Method == "POST" {
            createUser(w, r)
        }
    default:
        http.NotFound(w, r)
    }
}

func getUsers(w http.ResponseWriter, r *http.Request) {
    rows, err := db.Query("SELECT id, name, email, created_at FROM users ORDER BY id")
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
    defer rows.Close()

    var users []User
    for rows.Next() {
        var u User
        rows.Scan(&u.ID, &u.Name, &u.Email, &u.CreatedAt)
        users = append(users, u)
    }

    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(users)
}

func createUser(w http.ResponseWriter, r *http.Request) {
    var u User
    if err := json.NewDecoder(r.Body).Decode(&u); err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }

    result, err := db.Exec("INSERT INTO users (name, email) VALUES (?, ?)", u.Name, u.Email)
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }

    id, _ := result.LastInsertId()
    u.ID = int(id)

    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(http.StatusCreated)
    json.NewEncoder(w).Encode(u)
}

func corsMiddleware(next http.HandlerFunc) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Access-Control-Allow-Origin", "*")
        w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
        w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

        if r.Method == "OPTIONS" {
            w.WriteHeader(http.StatusOK)
            return
        }

        next(w, r)
    }
}
```

**Deploy Backend**:

```bash
# Deploy the source directory — the CLI cross-compiles
# for linux/amd64 (CGO_ENABLED=0) and uploads for you
orama deploy go . --name myapp-api
```

### Step 3: Deploy React Frontend

**Frontend Code** (`src/App.jsx`):

```jsx
import { useEffect, useState } from 'react';

function App() {
  const [users, setUsers] = useState([]);
  const [name, setName] = useState('');
  const [email, setEmail] = useState('');

  const API_URL = 'https://myapp-api.orama.network';

  useEffect(() => {
    fetchUsers();
  }, []);

  const fetchUsers = async () => {
    const response = await fetch(`${API_URL}/api/users`);
    const data = await response.json();
    setUsers(data);
  };

  const addUser = async (e) => {
    e.preventDefault();
    await fetch(`${API_URL}/api/users`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name, email }),
    });
    setName('');
    setEmail('');
    fetchUsers();
  };

  return (
    <div>
      <h1>Orama Network Full-Stack App</h1>

      <h2>Add User</h2>
      <form onSubmit={addUser}>
        <input
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="Name"
          required
        />
        <input
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          placeholder="Email"
          type="email"
          required
        />
        <button type="submit">Add User</button>
      </form>

      <h2>Users</h2>
      <ul>
        {users.map((user) => (
          <li key={user.id}>
            {user.name} - {user.email}
          </li>
        ))}
      </ul>
    </div>
  );
}

export default App;
```

**Deploy Frontend**:

```bash
# Build
npm run build

# Deploy
orama deploy static ./dist --name myapp
```

### Step 4: Access Your App

Open your browser to:
- **Frontend**: `https://myapp.orama.network`
- **Backend API**: `https://myapp-api.orama.network/api/users`

### Full-Stack Summary

✅ **Frontend**: React app served from IPFS
✅ **Backend**: Go API running on allocated port
✅ **Database**: SQLite database with ACID transactions
✅ **Domains**: Automatic DNS for both services
✅ **Isolated**: All resources namespaced and secure

---

## Managing Deployments

### List All Deployments

```bash
orama app list

# Output:
# NAME              TYPE      STATUS    VERSION    CREATED
# my-react-app      static    active    1          2024-01-22 10:30
# myapp-api         go        active    1          2024-01-22 10:45
# my-nextjs         nextjs    active    2          2024-01-22 11:00
#
# Total: 3
```

### Get Deployment Details

```bash
orama app get my-react-app

# Output:
# Deployment: my-react-app
#
# ID:               dep-abc123
# Type:             static
# Status:           active
# Version:          1
# Namespace:        your-namespace
# Content CID:      QmXxxx...
# Memory Limit:     256 MB
# CPU Limit:        50%
# Restart Policy:   always
#
# URLs:
#   • https://my-react-app.orama.network
#
# Created:          2024-01-22T10:30:00Z
# Updated:          2024-01-22T10:30:00Z
```

### View Logs

```bash
# View last 100 lines
orama app logs my-nextjs

# Follow logs in real-time
orama app logs my-nextjs --follow
```

### Rollback to Previous Version

```bash
# Rollback to version 1
orama app rollback my-nextjs --version 1

# Output:
# ⚠️  Rolling back 'my-nextjs' to version 1. Continue? (y/N): y
#
# ✅ Rollback successful!
#
# Deployment:       my-nextjs
# Current Version:  1
# Rolled Back From: 2
# Rolled Back To:   1
# Status:           active
```

### Delete Deployment

```bash
orama app delete my-old-app

# Output:
# ⚠️  Are you sure you want to delete deployment 'my-old-app'? (y/N): y
#
# ✅ Deployment 'my-old-app' deleted successfully
```

---

## WebRTC (Voice/Video/Data)

Namespaces can enable WebRTC support for real-time communication (voice calls, video calls, data channels).

### Enable WebRTC

```bash
# Enable WebRTC for a namespace (must be run on a cluster node)
orama namespace enable webrtc --namespace myapp

# Check WebRTC status
orama namespace webrtc-status --namespace myapp
```

This provisions SFU servers on all 3 nodes and TURN relay servers on up to 2 nodes,
allocates port blocks, creates DNS records, and opens firewall ports.

> **TURN is shared per host.** TURN binds the fixed ports 3478/5349, which are
> exclusive per physical host, so every namespace allocated TURN on a node is served
> by that node's single `orama-turn.service`. Each namespace authenticates against
> its own shared secret — the TURN credential already carries the namespace
> (`{expiry}:{namespace}`) — so tenants never share a relay identity, and a namespace
> the server does not serve is rejected outright.
>
> Adding or removing a namespace rewrites that host's `configs/turn.yaml`, which the
> running server re-reads within ~15s. It is **not** restarted: a restart would drop
> every other namespace's live relays on that host.
>
> A namespace can therefore get TURN on any node, regardless of what else runs there.
> `orama namespace webrtc-status` reports the number actually running.

### Disable WebRTC

```bash
orama namespace disable webrtc --namespace myapp
```

Stops all SFU/TURN services, deallocates ports, removes DNS records, and closes firewall ports.

### Client Integration

```javascript
// 1. Get TURN credentials
const creds = await fetch('https://ns-myapp.orama.network/v1/webrtc/turn/credentials', {
  method: 'POST',
  headers: { 'Authorization': `Bearer ${jwt}` }
});
const { urls, username, credential, ttl } = await creds.json();

// 2. Create PeerConnection (forced relay)
const pc = new RTCPeerConnection({
  iceServers: [{ urls, username, credential }],
  iceTransportPolicy: 'relay'
});

// 3. Connect signaling WebSocket
const ws = new WebSocket(
  `wss://ns-myapp.orama.network/v1/webrtc/signal?room=${roomId}`,
  ['Bearer', jwt]
);
```

See [docs/WEBRTC.md](WEBRTC.md) for the full API reference, room management, credential protocol, and debugging guide.

---

## Troubleshooting

### Deployment Issues

**Problem**: Deployment status is "failed"

```bash
# Check deployment details
orama app get my-app

# View logs for errors
orama app logs my-app

# Common issues:
# - App not listening on the PORT environment variable
# - Missing dependencies (not declared in package.json / go.mod)
# - Port already in use (shouldn't happen, but check logs)
# - Health check failing (ensure /health endpoint exists)
```

**Problem**: Can't access deployment URL

```bash
# 1. Check deployment status
orama app get my-app

# 2. Verify DNS (may take up to 10 seconds to propagate)
dig my-app.orama.network

# 3. For local development, add to /etc/hosts
echo "127.0.0.1 my-app.orama.network" | sudo tee -a /etc/hosts

# 4. Test with Host header
curl -H "Host: my-app.orama.network" http://localhost:6001/
```

### Database Issues

**Problem**: Database not found

```bash
# List all databases
orama db list

# Ensure database name matches exactly (case-sensitive)
# Databases are namespace-isolated
```

**Problem**: SQL query fails

```bash
# Check table exists
orama db query my-db "SELECT name FROM sqlite_master WHERE type='table'"

# Check table schema (sqlite3 dot-commands like .schema are NOT supported — plain SQL only)
orama db query my-db "SELECT sql FROM sqlite_master WHERE name='users'"
```

### Authentication Issues

```bash
# Re-authenticate
orama auth logout
orama auth login

# Check token validity
orama auth status
```

### Need Help?

- **Documentation**: Check `/docs` directory
- **Logs**: Gateway logs at `~/.orama/logs/gateway.log`
- **Issues**: Report bugs at GitHub repository
- **Community**: Join our Discord/Telegram

---

## Best Practices

### Security

1. **Never commit sensitive data**: Use environment variables for secrets
2. **Validate inputs**: Always sanitize user input in your backend
3. **HTTPS only**: All deployments automatically use HTTPS in production
4. **CORS**: Configure CORS appropriately for your API

### Performance

1. **Optimize builds**: Minimize bundle sizes (React, Next.js)
2. **Use caching**: Leverage browser caching for static assets
3. **Database indexes**: Add indexes to frequently queried columns
4. **Health checks**: Implement `/health` endpoint for monitoring

### Deployment Workflow

1. **Test locally first**: Ensure your app works before deploying
2. **Use version control**: Track changes in Git
3. **Incremental updates**: Use `--update` flag instead of delete + redeploy
4. **Backup databases**: Regular backups via `orama db backup`
5. **Monitor logs**: Check logs after deployment for errors

---

## Next Steps

- **Explore the API**: See `/docs/GATEWAY_API.md` for HTTP API details
- **Advanced Features**: Custom domains (via gateway API, see [How Domains Work](#how-domains-work)); load balancing and autoscaling coming soon
- **Production Deployment**: Install nodes with `orama node install` for production clusters
- **Client SDK**: Use the Go/JS SDK for programmatic deployments

---

**Orama Network** - Decentralized Application Platform

Deploy anywhere. Access everywhere. Own everything.
