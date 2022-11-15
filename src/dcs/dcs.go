package dcs

import "context"

const (
	hostnameKey = "hostname"
	roleKey     = "role"
)

type DCS interface {
	Connect(ctx context.Context) error
	GetRole(ctx context.Context) (string, error)
	StartElection(ctx context.Context) error
	SaveInstanceInfo(ctx context.Context, role string) error
	GetLeaderInfo(ctx context.Context) (InstanceInfo, error)
	GetClusterInstancesInfo(ctx context.Context) ([]InstanceInfo, error)
	Promote(ctx context.Context, candidateInstanceID string) error
	Shutdown(ctx context.Context) error
}

type InstanceInfo struct {
	ID       string `json:"id"`
	Role     string `json:"role"`
	Hostname string `json:"hostname"`
}

type Config struct {
	Hostname   string
	InstanceID string
	Lease      int
	Namespace  string
}
