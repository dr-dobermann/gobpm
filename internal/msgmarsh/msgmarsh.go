package msgmarsh

import "time"

// VarMarsh used as a Variable wrap while Message transmission.
type VarMarsh struct {
	Name      string `json:"name"`
	Type      uint8  `json:"var_type"`
	Precision int    `json:"precision"`
	Value     struct {
		Int    int64     `json:"int"`
		Bool   bool      `json:"bool"`
		String string    `json:"string"`
		Float  float64   `json:"float"`
		Time   time.Time `json:"time"`
	} `json:"value"`
}

// Message wrapper for transmission
type MsgMarsh struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Direction uint8  `json:"direction"`
	Variables []struct {
		Optional bool     `json:"optional"`
		Variable VarMarsh `json:"variable"`
	} `json:"vars"`
}
