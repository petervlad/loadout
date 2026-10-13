package api

import "time"

// MemoryTokenRequest is the body of POST /v1/memory/tokens.
type MemoryTokenRequest struct {
	// Label says where the token is used, such as "laptop".
	Label string `json:"label,omitempty"`
}

// MemoryToken is one memory token. Token holds the secret and is set only in
// the answer to the POST that made it, since the registry keeps no copy.
type MemoryToken struct {
	ID        string    `json:"id"`
	Label     string    `json:"label,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	Token     string    `json:"token,omitempty"`
}

// MemoryTokensResponse is the body of GET /v1/memory/tokens, newest first.
type MemoryTokensResponse struct {
	Tokens []MemoryToken `json:"tokens"`
}

// MemorySpace is a namespace where the caller has memory rights and the
// switch is on.
type MemorySpace struct {
	Namespace string    `json:"namespace"`
	Kind      LayerKind `json:"kind"`
	// Endpoint is the full URL of the namespace's MCP server.
	Endpoint string `json:"endpoint"`
}

// MemorySpacesResponse is the body of GET /v1/memory/spaces.
type MemorySpacesResponse struct {
	Spaces []MemorySpace `json:"spaces"`
}
