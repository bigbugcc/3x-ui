package database

import "sync/atomic"

// AuthenticationSuspended prevents identities from a partially restored database
// from being accepted before the durable authentication recovery has completed.
var AuthenticationSuspended atomic.Bool
