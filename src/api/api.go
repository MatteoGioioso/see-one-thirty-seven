package api

import (
	"context"
	"fmt"
	"github.com/MatteoGioioso/seeonethirtyseven/dcs_proxy"
	"github.com/MatteoGioioso/seeonethirtyseven/postgresql"
	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"net/http"
)

type Config struct {
	Port string
}

type Api struct {
	Postmaster postgresql.Postmaster
	DcsProxy   dcs_proxy.ProxyImpl
	Log        *logrus.Entry
	Config
}

func (s *Api) Start(ctx context.Context) {
	gin.SetMode(gin.ReleaseMode)
	r := gin.Default()
	s.Log = s.Log.WithField("subcomponent", "api")
	s.Log.Infof("starting seeone api")

	r.GET("/switchover", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"message": "pong",
		})
	})

	if err := r.Run(fmt.Sprintf(":%v", s.Port)); err != nil {
		s.Log.WithError(err).Fatal()
	}
}

// If the DCS is not reachable, then we should not allow any manual operation to avoid inconsistency in the cluster state
func (s *Api) shouldAPIBeBlocked(ctx context.Context) (bool, error) {
	if _, err := s.DcsProxy.GetRole(ctx); err != nil {
		return true, err
	}

	return false, nil
}
