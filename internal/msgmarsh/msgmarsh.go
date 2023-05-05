package msgmarsh

// Message wrapper for transmission
type MsgMarsh[T any] struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Item T      `json:"item"`
}
