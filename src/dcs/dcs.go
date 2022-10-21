package dcs

type DCS interface {
}

type InstanceInfo struct {
	ID       string `json:"id"`
	Role     string `json:"role"`
	Hostname string `json:"hostname"`
}
