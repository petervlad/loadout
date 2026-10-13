package memory

import "context"

// Roles answers Rights from memberships and the namespace tree.
// account.Roles implements it. An unknown namespace and one the person is not
// in must answer the same, so nothing reveals whether a team exists.
type Roles interface {
	MemoryRights(ctx context.Context, userID, namespace string) (Rights, error)
}
