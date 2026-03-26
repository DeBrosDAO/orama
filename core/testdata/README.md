# E2E Test Fixtures

This directory contains test applications used for end-to-end testing of the Orama Network deployment system.

## Test Applications

### 1. React Vite App (`apps/react-vite/`)
A minimal React application built with Vite for testing static site deployments.

**Features:**
- Simple counter component
- CSS and JavaScript assets
- Test markers for E2E validation

**Build:**
```bash
cd apps/react-vite
npm install
npm run build
# Output: dist/
```

### 2. Next.js SSR App (`apps/nextjs-ssr/`)
A Next.js application with server-side rendering and API routes for testing dynamic deployments.

**Features:**
- Server-side rendered homepage
- API routes:
  - `/api/hello` - Simple greeting endpoint
  - `/api/data` - JSON data with users list
- TypeScript support

**Build:**
```bash
cd apps/nextjs-ssr
npm install
npm run build
# Output: .next/
```

### 3. Go Backend (`apps/go-backend/`)
A simple Go HTTP API for testing native backend deployments.

**Features:**
- Health check endpoint: `/health`
- Users API: `/api/users` (GET, POST)
- Environment variable support (PORT)

**Build:**
```bash
cd apps/go-backend
make build
# Output: api (Linux binary)
```

## Building All Fixtures

Use the build script to create deployment-ready tarballs for all test apps:

```bash
./build-fixtures.sh
```

This will:
1. Build all three applications
2. Create compressed tarballs in `tarballs/`:
   - `react-vite.tar.gz` - Static site deployment
   - `nextjs-ssr.tar.gz` - Next.js SSR deployment
   - `go-backend.tar.gz` - Go backend deployment

## Tarballs

Pre-built deployment artifacts are stored in `tarballs/` for use in E2E tests.

**Usage in tests:**
```go
tarballPath := filepath.Join("../../testdata/tarballs/react-vite.tar.gz")
file, err := os.Open(tarballPath)
// Upload to deployment endpoint
```

## Directory Structure

```
testdata/
├── apps/                          # Source applications
│   ├── react-vite/               # React + Vite static app
│   ├── nextjs-ssr/               # Next.js SSR app
│   └── go-backend/               # Go HTTP API
│
├── tarballs/                      # Deployment artifacts
│   ├── react-vite.tar.gz
│   ├── nextjs-ssr.tar.gz
│   └── go-backend.tar.gz
│
├── build-fixtures.sh             # Build script
└── README.md                     # This file
```

## Development

To modify test apps:

1. Edit source files in `apps/{app-name}/`
2. Run `./build-fixtures.sh` to rebuild
3. Tarballs are automatically updated for E2E tests

## Testing Locally

### React Vite App
```bash
cd apps/react-vite
npm run dev
# Open http://localhost:5173
```

### Next.js App
```bash
cd apps/nextjs-ssr
npm run dev
# Open http://localhost:3000
# Test API: http://localhost:3000/api/hello
```

### Go Backend
```bash
cd apps/go-backend
go run main.go
# Test: curl http://localhost:8080/health
# Test: curl http://localhost:8080/api/users
```

## Notes

- All apps are intentionally minimal to ensure fast build and deployment times
- React and Next.js apps include test markers (`data-testid`) for E2E validation
- Go backend uses standard library only (no external dependencies)
- Build script requires: Node.js (18+), npm, Go (1.21+), tar, gzip
