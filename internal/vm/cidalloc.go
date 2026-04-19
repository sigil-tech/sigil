package vm

import (
	"context"
	"fmt"
)

// cidMin is the lowest CID available for allocation. CIDs 0–2 are reserved
// by the vsock spec (VMADDR_CID_ANY=0xffffffff, VMADDR_CID_HYPERVISOR=0,
// VMADDR_CID_LOCAL=1, VMADDR_CID_HOST=2). ADR-002 §3 reserves CID 100 as the
// development CID; production allocations begin at 101.
const cidMin uint32 = 3

// cidMax is the highest CID available for allocation. The vsock address space
// is uint32; we exclude the broadcast value 2^32-1.
const cidMax uint32 = 1<<32 - 2

// AllocCID reserves the next available CID in [cidMin, cidMax] that is not
// currently held by an active session (status IN booting/ready/connecting/
// stopping). The allocation is crash-safe: on daemon restart a fresh table
// scan recovers the in-use set — no in-memory state needs rebuilding.
//
// Returns ERR_HYPERVISOR_UNAVAILABLE if the entire CID range is exhausted.
func (m *Manager) AllocCID(ctx context.Context) (uint32, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rows, err := m.db.QueryContext(ctx,
		`SELECT vsock_cid FROM sessions
		 WHERE status IN ('booting','ready','connecting','stopping')
		   AND vsock_cid != 0`,
	)
	if err != nil {
		return 0, fmt.Errorf("vm: AllocCID query active CIDs: %w", err)
	}
	defer rows.Close()

	inUse := make(map[uint32]bool)
	for rows.Next() {
		var cid int64
		if err := rows.Scan(&cid); err != nil {
			return 0, fmt.Errorf("vm: AllocCID scan: %w", err)
		}
		if cid > 0 {
			inUse[uint32(cid)] = true
		}
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("vm: AllocCID rows: %w", err)
	}

	for cid := cidMin; cid <= cidMax; cid++ {
		if !inUse[cid] {
			return cid, nil
		}
	}

	return 0, &VMError{
		Code:    ErrHypervisorUnavailable,
		Message: "all vsock CIDs in range are in use",
	}
}

// FreeCID explicitly releases a CID on error paths where the session row was
// not committed. Under normal teardown, CIDs are implicitly available once the
// session reaches a terminal state (AllocCID's table-scan skips terminal rows).
// FreeCID is a no-op if no active session holds the given CID — it is safe to
// call on any CID value, including 0.
func (m *Manager) FreeCID(ctx context.Context, cid uint32) error {
	if cid == 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	_, err := m.db.ExecContext(ctx,
		`UPDATE sessions SET status = ?, vsock_cid = 0
		 WHERE vsock_cid = ?
		   AND status IN ('booting','ready','connecting','stopping')`,
		string(StateFailed), int64(cid),
	)
	if err != nil {
		return fmt.Errorf("vm: FreeCID %d: %w", cid, err)
	}
	return nil
}
