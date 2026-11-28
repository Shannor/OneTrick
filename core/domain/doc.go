// Package domain holds shared, pure value-semantic domain models (such as User, Character,
// Membership, Session, Snapshot, Loadout, and Aggregate) and pure invariant functions/methods
// shared across peer core services and driven adapters.
//
// Architectural invariants (enforced by arch_test.go):
//   - Zero driven/driving ports and zero I/O.
//   - Zero transport or persistence struct tags (json, firestore) and zero external SDK imports.
//   - Leaf core package: never imports sibling core/<service> or core/<workflow> packages.
package domain
