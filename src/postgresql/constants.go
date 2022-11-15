package postgresql

const (
	Leader               = "leader"
	Replica              = "replica"
	LeaderElectionPrefix = "/postgresql-leader"
	InstanceInfoPrefix   = "/postgresql-info"
	ReplicationSlot      = "replication"

	StopModeSmart     = "smart"     // disallows new connections, then waits for all existing clients to disconnect
	StopModeFast      = "fast"      // (the default) does not wait for clients to disconnect. All active transactions are rolled back and clients are forcibly disconnected
	StopModeImmediate = "immediate" // abort all server processes immediately, without a clean shutdown. This choice will lead to a crash-recovery cycle during the next server start
)
