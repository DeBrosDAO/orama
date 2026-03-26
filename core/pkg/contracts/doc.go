// Package contracts defines clean, focused interface contracts for the Orama Network.
//
// This package follows the Interface Segregation Principle (ISP) by providing
// small, focused interfaces that define clear contracts between components.
// Each interface represents a specific capability or service without exposing
// implementation details.
//
// Design Principles:
//   - Small, focused interfaces (ISP compliance)
//   - No concrete type leakage in signatures
//   - Comprehensive documentation for all public methods
//   - Domain-aligned contracts (storage, cache, database, auth, serverless, etc.)
//
// Interfaces:
//   - StorageProvider: Decentralized content storage (IPFS)
//   - CacheProvider/CacheClient: Distributed caching (Olric)
//   - DatabaseClient: ORM-like database operations (RQLite)
//   - AuthService: Wallet-based authentication and JWT management
//   - FunctionExecutor: WebAssembly function execution
//   - FunctionRegistry: Function metadata and bytecode storage
//   - PubSubService: Topic-based messaging
//   - PeerDiscovery: Peer discovery and connection management
//   - Logger: Structured logging
package contracts
