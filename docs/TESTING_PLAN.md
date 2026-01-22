# Comprehensive Testing Plan

This document outlines the complete testing strategy for the namespace isolation and custom deployment system.

## Table of Contents

1. [Unit Tests](#unit-tests)
2. [Integration Tests](#integration-tests)
3. [End-to-End Tests](#end-to-end-tests)
4. [CLI Tests](#cli-tests)
5. [Performance Tests](#performance-tests)
6. [Security Tests](#security-tests)
7. [Chaos/Failure Tests](#chaos-failure-tests)

---

## 1. Unit Tests

### 1.1 Port Allocator Tests

**File**: `pkg/deployments/port_allocator_test.go`

**Test Cases**:
- ✅ Allocate first port (should be 10100)
- ✅ Allocate sequential ports
- ✅ Find gaps in allocation
- ✅ Handle port exhaustion (all 10000 ports used)
- ✅ Concurrent allocation with race detector
- ✅ Conflict retry with exponential backoff

**Command**:
```bash
go test ./pkg/deployments -run TestPortAllocator -v
```

### 1.2 Home Node Manager Tests

**File**: `pkg/deployments/home_node_test.go`

**Test Cases**:
- ✅ Assign namespace to node with lowest load
- ✅ Reuse existing home node for namespace
- ✅ Weight calculation (deployments, ports, memory, CPU)
- ✅ Handle no nodes available
- ✅ Node failure detection and reassignment

**Command**:
```bash
go test ./pkg/deployments -run TestHomeNodeManager -v
```

### 1.3 Health Checker Tests

**File**: `pkg/deployments/health/checker_test.go` (needs to be created)

**Test Cases**:
- Check static deployment (always healthy)
- Check dynamic deployment with health endpoint
- Mark as failed after 3 consecutive failures
- Record health check history
- Parallel health checking

**Command**:
```bash
go test ./pkg/deployments/health -v
```

### 1.4 Process Manager Tests

**File**: `pkg/deployments/process/manager_test.go` (needs to be created)

**Test Cases**:
- Create systemd service file
- Start deployment process
- Stop deployment process
- Restart deployment
- Read logs from journalctl

**Command**:
```bash
go test ./pkg/deployments/process -v
```

### 1.5 Deployment Service Tests

**File**: `pkg/gateway/handlers/deployments/service_test.go` (needs to be created)

**Test Cases**:
- Create deployment
- Get deployment by ID
- List deployments for namespace
- Update deployment
- Delete deployment
- Record deployment history

**Command**:
```bash
go test ./pkg/gateway/handlers/deployments -v
```

---

## 2. Integration Tests

### 2.1 Static Deployment Integration Test

**File**: `tests/integration/static_deployment_test.go` (needs to be created)

**Setup**:
- Start test RQLite instance
- Start test IPFS node
- Start test gateway

**Test Flow**:
1. Upload static content tarball
2. Verify deployment created in database
3. Verify content uploaded to IPFS
4. Verify DNS record created
5. Test HTTP request to deployment domain
6. Verify content served correctly

**Command**:
```bash
go test ./tests/integration -run TestStaticDeployment -v
```

### 2.2 Next.js SSR Deployment Integration Test

**File**: `tests/integration/nextjs_deployment_test.go` (needs to be created)

**Test Flow**:
1. Upload Next.js build
2. Verify systemd service created
3. Verify process started
4. Wait for health check to pass
5. Test HTTP request to deployment
6. Verify SSR response

**Command**:
```bash
go test ./tests/integration -run TestNextJSDeployment -v
```

### 2.3 SQLite Database Integration Test

**File**: `tests/integration/sqlite_test.go` (needs to be created)

**Test Flow**:
1. Create SQLite database
2. Verify database file created on disk
3. Execute CREATE TABLE query
4. Execute INSERT query
5. Execute SELECT query
6. Backup database to IPFS
7. Verify backup CID recorded

**Command**:
```bash
go test ./tests/integration -run TestSQLiteDatabase -v
```

### 2.4 Custom Domain Integration Test

**File**: `tests/integration/custom_domain_test.go` (needs to be created)

**Test Flow**:
1. Add custom domain to deployment
2. Verify TXT record verification token generated
3. Mock DNS TXT record lookup
4. Verify domain
5. Verify DNS A record created
6. Test HTTP request to custom domain

**Command**:
```bash
go test ./tests/integration -run TestCustomDomain -v
```

### 2.5 Update and Rollback Integration Test

**File**: `tests/integration/update_rollback_test.go` (needs to be created)

**Test Flow**:
1. Deploy initial version
2. Update deployment with new content
3. Verify version incremented
4. Verify new content served
5. Rollback to previous version
6. Verify old content served
7. Verify history recorded correctly

**Command**:
```bash
go test ./tests/integration -run TestUpdateRollback -v
```

---

## 3. End-to-End Tests

### 3.1 Full Static Deployment E2E Test

**File**: `tests/e2e/static_deployment_test.go` (needs to be created)

**Prerequisites**:
- Running RQLite cluster
- Running IPFS cluster
- Running gateway instances
- CoreDNS configured

**Test Flow**:
1. Create test React app
2. Build app (`npm run build`)
3. Deploy via CLI: `orama deploy static ./dist --name e2e-static`
4. Wait for deployment to be active
5. Resolve deployment domain via DNS
6. Make HTTPS request to deployment
7. Verify all static assets load correctly
8. Test SPA routing (fallback to index.html)
9. Update deployment
10. Verify zero-downtime update
11. Delete deployment
12. Verify cleanup

**Command**:
```bash
go test ./tests/e2e -run TestStaticDeploymentE2E -v -timeout 10m
```

### 3.2 Full Next.js SSR Deployment E2E Test

**File**: `tests/e2e/nextjs_deployment_test.go` (needs to be created)

**Test Flow**:
1. Create test Next.js app with API routes
2. Build app (`npm run build`)
3. Deploy via CLI: `orama deploy nextjs . --name e2e-nextjs --ssr`
4. Wait for process to start and pass health check
5. Test static route
6. Test API route
7. Test SSR page
8. Update deployment with graceful restart
9. Verify health check before cutting over
10. Rollback if health check fails
11. Monitor logs during deployment
12. Delete deployment

**Command**:
```bash
go test ./tests/e2e -run TestNextJSDeploymentE2E -v -timeout 15m
```

### 3.3 Full SQLite Database E2E Test

**File**: `tests/e2e/sqlite_test.go` (needs to be created)

**Test Flow**:
1. Create database via CLI: `orama db create e2e-testdb`
2. Create schema: `orama db query e2e-testdb "CREATE TABLE ..."`
3. Insert data: `orama db query e2e-testdb "INSERT ..."`
4. Query data: `orama db query e2e-testdb "SELECT ..."`
5. Verify results match expected
6. Backup database: `orama db backup e2e-testdb`
7. List backups: `orama db backups e2e-testdb`
8. Verify backup CID in IPFS
9. Restore from backup (if implemented)
10. Verify data integrity

**Command**:
```bash
go test ./tests/e2e -run TestSQLiteDatabaseE2E -v -timeout 10m
```

### 3.4 DNS Resolution E2E Test

**File**: `tests/e2e/dns_test.go` (needs to be created)

**Test Flow**:
1. Create deployment
2. Query all 4 nameservers for deployment domain
3. Verify all return same IP
4. Add custom domain
5. Verify TXT record
6. Verify A record created
7. Query external DNS resolver
8. Verify domain resolves correctly

**Command**:
```bash
go test ./tests/e2e -run TestDNSResolutionE2E -v -timeout 5m
```

---

## 4. CLI Tests

### 4.1 Deploy Command Tests

**File**: `tests/cli/deploy_test.go` (needs to be created)

**Test Cases**:
- Deploy static site
- Deploy Next.js with --ssr flag
- Deploy Node.js backend
- Deploy Go backend
- Handle missing arguments
- Handle invalid paths
- Handle network errors gracefully

**Command**:
```bash
go test ./tests/cli -run TestDeployCommand -v
```

### 4.2 Deployments Management Tests

**File**: `tests/cli/deployments_test.go` (needs to be created)

**Test Cases**:
- List all deployments
- Get specific deployment
- Delete deployment with confirmation
- Rollback to version
- View logs with --follow
- Filter deployments by status

**Command**:
```bash
go test ./tests/cli -run TestDeploymentsCommands -v
```

### 4.3 Database Command Tests

**File**: `tests/cli/db_test.go` (needs to be created)

**Test Cases**:
- Create database
- Execute query (SELECT, INSERT, UPDATE, DELETE)
- List databases
- Backup database
- List backups
- Handle SQL syntax errors
- Handle connection errors

**Command**:
```bash
go test ./tests/cli -run TestDatabaseCommands -v
```

### 4.4 Domain Command Tests

**File**: `tests/cli/domain_test.go` (needs to be created)

**Test Cases**:
- Add custom domain
- Verify domain
- List domains
- Remove domain
- Handle verification failures

**Command**:
```bash
go test ./tests/cli -run TestDomainCommands -v
```

---

## 5. Performance Tests

### 5.1 Concurrent Deployment Test

**Objective**: Verify system handles multiple concurrent deployments

**Test**:
```bash
# Deploy 50 static sites concurrently
for i in {1..50}; do
  orama deploy static ./test-site --name test-$i &
done
wait

# Verify all succeeded
orama deployments list | grep -c "active"
# Should output: 50
```

### 5.2 Port Allocation Performance Test

**Objective**: Measure port allocation speed under high contention

**Test**:
```go
func BenchmarkPortAllocation(b *testing.B) {
    // Setup
    db := setupTestDB()
    allocator := deployments.NewPortAllocator(db, logger)

    b.RunParallel(func(pb *testing.PB) {
        for pb.Next() {
            _, err := allocator.AllocatePort(ctx, "test-node", uuid.New().String())
            if err != nil {
                b.Fatal(err)
            }
        }
    })
}
```

**Command**:
```bash
go test -bench=BenchmarkPortAllocation -benchtime=10s ./pkg/deployments
```

### 5.3 DNS Query Performance Test

**Objective**: Measure CoreDNS query latency with RQLite backend

**Test**:
```bash
# Warm up
for i in {1..1000}; do
  dig @localhost test.orama.network > /dev/null
done

# Benchmark
ab -n 10000 -c 100 http://localhost:53/dns-query?name=test.orama.network

# Expected: <50ms p95 latency
```

### 5.4 Health Check Performance Test

**Objective**: Verify health checker handles 1000 deployments

**Test**:
- Create 1000 test deployments
- Start health checker
- Measure time to complete one check cycle
- Expected: <60 seconds for all 1000 checks

---

## 6. Security Tests

### 6.1 Namespace Isolation Test

**Objective**: Verify users cannot access other namespaces' resources

**Test**:
```bash
# User A deploys
export ORAMA_TOKEN="user-a-token"
orama deploy static ./site --name myapp

# User B attempts to access User A's deployment
export ORAMA_TOKEN="user-b-token"
orama deployments get myapp
# Expected: 404 Not Found or 403 Forbidden

# User B attempts to access User A's database
orama db query user-a-db "SELECT * FROM users"
# Expected: 404 Not Found or 403 Forbidden
```

### 6.2 SQL Injection Test

**Objective**: Verify SQLite handler sanitizes inputs

**Test**:
```bash
# Attempt SQL injection in database name
orama db create "test'; DROP TABLE users; --"
# Expected: Validation error

# Attempt SQL injection in query
orama db query testdb "SELECT * FROM users WHERE id = '1' OR '1'='1'"
# Expected: Query executes safely (parameterized)
```

### 6.3 Path Traversal Test

**Objective**: Verify deployment paths are sanitized

**Test**:
```bash
# Attempt path traversal in deployment name
orama deploy static ./site --name "../../etc/passwd"
# Expected: Validation error

# Attempt path traversal in SQLite database name
orama db create "../../../etc/shadow"
# Expected: Validation error
```

### 6.4 Resource Exhaustion Test

**Objective**: Verify resource limits are enforced

**Test**:
```bash
# Deploy 10001 sites (exceeds default limit)
for i in {1..10001}; do
  orama deploy static ./site --name test-$i
done
# Expected: Last deployment rejected with quota error

# Create huge SQLite database
orama db query bigdb "CREATE TABLE huge (data TEXT)"
orama db query bigdb "INSERT INTO huge VALUES ('$(head -c 10G </dev/urandom | base64)')"
# Expected: Size limit enforced
```

---

## 7. Chaos/Failure Tests

### 7.1 Node Failure Test

**Objective**: Verify system handles gateway node failure

**Test**:
1. Deploy app to node A
2. Verify deployment is healthy
3. Simulate node A crash: `systemctl stop orama-gateway`
4. Wait for failure detection
5. Verify namespace migrated to node B
6. Verify deployment restored from IPFS backup
7. Verify health checks pass on node B

### 7.2 RQLite Failure Test

**Objective**: Verify graceful degradation when RQLite is unavailable

**Test**:
1. Deploy app successfully
2. Stop RQLite: `systemctl stop rqlite`
3. Attempt new deployment (should fail gracefully with error message)
4. Verify existing deployments still serve traffic (cached)
5. Restart RQLite
6. Verify new deployments work

### 7.3 IPFS Failure Test

**Objective**: Verify handling of IPFS unavailability

**Test**:
1. Deploy static site
2. Stop IPFS: `systemctl stop ipfs`
3. Attempt to serve deployment (should fail or serve from cache)
4. Attempt new deployment (should fail with clear error)
5. Restart IPFS
6. Verify recovery

### 7.4 CoreDNS Failure Test

**Objective**: Verify DNS redundancy

**Test**:
1. Stop 1 CoreDNS instance
2. Verify DNS still resolves (3 of 4 servers working)
3. Stop 2nd CoreDNS instance
4. Verify DNS still resolves (2 of 4 servers working)
5. Stop 3rd CoreDNS instance
6. Verify DNS degraded but functional (1 of 4 servers)
7. Stop all 4 CoreDNS instances
8. Verify DNS resolution fails

### 7.5 Concurrent Update Test

**Objective**: Verify race conditions are handled

**Test**:
```bash
# Update same deployment concurrently
orama deploy static ./site-v2 --name myapp --update &
orama deploy static ./site-v3 --name myapp --update &
wait

# Verify only one update succeeded
# Verify database is consistent
# Verify no partial updates
```

---

## Test Execution Plan

### Phase 1: Unit Tests (Day 1)
- Run all unit tests
- Ensure 100% pass rate
- Measure code coverage (target: >80%)

### Phase 2: Integration Tests (Days 2-3)
- Run integration tests in isolated environment
- Fix any integration issues
- Verify database state consistency

### Phase 3: E2E Tests (Days 4-5)
- Run E2E tests in staging environment
- Test with real DNS, IPFS, RQLite
- Fix any environment-specific issues

### Phase 4: Performance Tests (Day 6)
- Run load tests
- Measure latency and throughput
- Optimize bottlenecks

### Phase 5: Security Tests (Day 7)
- Run security test suite
- Fix any vulnerabilities
- Document security model

### Phase 6: Chaos Tests (Day 8)
- Run failure scenario tests
- Verify recovery procedures
- Document failure modes

### Phase 7: Production Validation (Day 9-10)
- Deploy to production with feature flag OFF
- Run smoke tests in production
- Enable feature flag for 10% of traffic
- Monitor for 24 hours
- Gradually increase to 100%

---

## Test Environment Setup

### Local Development

```bash
# Start test dependencies
docker-compose -f tests/docker-compose.test.yml up -d

# Run unit tests
make test-unit

# Run integration tests
make test-integration

# Run all tests
make test-all
```

### CI/CD Pipeline

```yaml
# .github/workflows/test.yml
name: Test

on: [push, pull_request]

jobs:
  unit-tests:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v2
      - uses: actions/setup-go@v2
      - run: go test ./pkg/... -v -race -coverprofile=coverage.out

  integration-tests:
    runs-on: ubuntu-latest
    services:
      rqlite:
        image: rqlite/rqlite
      ipfs:
        image: ipfs/go-ipfs
    steps:
      - uses: actions/checkout@v2
      - uses: actions/setup-go@v2
      - run: go test ./tests/integration/... -v -timeout 15m

  e2e-tests:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v2
      - run: ./scripts/setup-test-env.sh
      - run: go test ./tests/e2e/... -v -timeout 30m
```

---

## Success Criteria

### Unit Tests
- ✅ All tests pass
- ✅ Code coverage >80%
- ✅ No race conditions detected

### Integration Tests
- ✅ All happy path scenarios pass
- ✅ Error scenarios handled gracefully
- ✅ Database state remains consistent

### E2E Tests
- ✅ Full workflows complete successfully
- ✅ DNS resolution works across all nameservers
- ✅ Deployments accessible via HTTPS

### Performance Tests
- ✅ Port allocation: <10ms per allocation
- ✅ DNS queries: <50ms p95 latency
- ✅ Deployment creation: <30s for static, <2min for dynamic
- ✅ Health checks: Complete 1000 deployments in <60s

### Security Tests
- ✅ Namespace isolation enforced
- ✅ No SQL injection vulnerabilities
- ✅ No path traversal vulnerabilities
- ✅ Resource limits enforced

### Chaos Tests
- ✅ Node failure: Recovery within 5 minutes
- ✅ Service failure: Graceful degradation
- ✅ Concurrent updates: No race conditions

---

## Ongoing Testing

After production deployment:

1. **Synthetic Monitoring**: Create test deployments every hour and verify they work
2. **Canary Deployments**: Test new versions with 1% of traffic before full rollout
3. **Load Testing**: Weekly load tests to ensure performance doesn't degrade
4. **Security Scanning**: Automated vulnerability scans
5. **Chaos Engineering**: Monthly chaos tests in staging

---

## Test Automation Commands

```bash
# Run all tests
make test-all

# Run unit tests only
make test-unit

# Run integration tests only
make test-integration

# Run E2E tests only
make test-e2e

# Run performance tests
make test-performance

# Run security tests
make test-security

# Run chaos tests
make test-chaos

# Generate coverage report
make test-coverage

# Run tests with race detector
make test-race
```
