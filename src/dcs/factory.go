package dcs

import "github.com/sirupsen/logrus"

type Factory struct {
	providers map[string]DCS
}

func NewFactory(endpoints []string, config Config, log *logrus.Entry) *Factory {
	return &Factory{
		map[string]DCS{
			"etcd":       NewEtcdImpl(endpoints, config, log),
			"kubernetes": NewKubernetes(config),
		},
	}
}

func (f Factory) Get(providerName string) DCS {
	return f.providers[providerName]
}
