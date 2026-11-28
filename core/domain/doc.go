// Package domain holds shared, pure value-semantic domain models (such as User, Character,
// Membership, Session, Snapshot, Loadout, and Aggregate) and pure invariant functions/methods
// shared across peer core services and driven adapters.
//
// Architectural invariants (enforced by arch_test.go):
//   - Zero driven/driving ports and zero I/O.
//   - Zero transport or persistence struct tags (json, firestore).
//   - Pure leaf package: zero third-party imports, only pure leaf internal helpers
//     (oneTrick/{set,utils,ptr}), and zero imports of sibling core/<service> or core/<workflow> packages.
package domain
