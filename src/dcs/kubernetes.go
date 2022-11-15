package dcs

import (
	"context"
	"github.com/MatteoGioioso/seeonethirtyseven/postgresql"
	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"time"
)

// https://itnext.io/leader-election-in-kubernetes-using-client-go-a19cbe7a9a85

type Kubernetes struct {
	Log *logrus.Entry

	kubeClient *clientset.Clientset
	elector    *leaderelection.LeaderElector
	lock       *resourcelock.LeaseLock

	instanceID string
	namespace  string
	hostname   string
	lease      int
}

func NewKubernetes(config Config) *Kubernetes {
	return &Kubernetes{
		instanceID: config.InstanceID,
		namespace:  config.Namespace,
		hostname:   config.Hostname,
		lease:      config.Lease,
	}
}

func (k *Kubernetes) Connect(ctx context.Context) error {
	config, err := rest.InClusterConfig()
	if err != nil {
		return err
	}

	kubeClient := clientset.NewForConfigOrDie(config)
	lock := &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Name:      postgresql.LeaderElectionPrefix,
			Namespace: k.namespace,
			Annotations: map[string]string{
				hostnameKey: k.hostname,
			},
		},
		Client: kubeClient.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: k.instanceID,
		},
	}
	elector, err := leaderelection.NewLeaderElector(leaderelection.LeaderElectionConfig{
		Lock:            lock,
		ReleaseOnCancel: true,
		LeaseDuration:   time.Duration(k.lease) * time.Second,
		RenewDeadline:   10 * time.Second,
		RetryPeriod:     2 * time.Second,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(c context.Context) {},
			OnStoppedLeading: func() {},
			OnNewLeader:      func(id string) {},
		},
	})
	if err != nil {
		return err
	}

	k.kubeClient = kubeClient
	k.lock = lock
	k.elector = elector

	return nil
}

func (k *Kubernetes) GetRole(ctx context.Context) (string, error) {
	if k.elector.IsLeader() {
		return postgresql.Leader, nil
	} else {
		return postgresql.Replica, nil
	}
}

func (k *Kubernetes) StartElection(ctx context.Context) error {
	k.elector.Run(ctx)
	return nil
}

func (k *Kubernetes) SaveInstanceInfo(ctx context.Context, role string) error {
	//TODO implement me
	panic("implement me")
}

func (k *Kubernetes) GetLeaderInfo(ctx context.Context) (InstanceInfo, error) {

	return InstanceInfo{}, nil
}

func (k *Kubernetes) GetClusterInstancesInfo(ctx context.Context) ([]InstanceInfo, error) {
	//TODO implement me
	panic("implement me")
}

func (k *Kubernetes) Promote(ctx context.Context, candidateInstanceID string) error {
	panic("implement me")
}

func (k *Kubernetes) Shutdown(ctx context.Context) error {
	panic("implement me")
}
