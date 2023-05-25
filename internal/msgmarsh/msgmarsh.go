package msgmarsh

// Message wrapper for transmission
type MsgMarsh struct {
	ID   string                 `json:"id"`
	Name string                 `json:"name"`
	Item map[string]interface{} `json:"item"`
}
