package models

// Struct to represent the JSON data
type Subscription struct {
	PubSubName string            `json:"pubsubname"`
	Topic      string            `json:"topic"`
	Metadata   map[string]string `json:"metadata,omitempty"`
	Route      PubSubRoute       `json:"route"`
}

type PubSubRoute struct {
	Rules   []RouteRule `json:"rules,omitempty"`
	Default string      `json:"default,omitempty"`
}

type RouteRule struct {
	Match string `json:"match"`
	Path  string `json:"path"`
}
