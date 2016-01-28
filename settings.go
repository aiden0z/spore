package spore

import (
	"time"

	log "github.com/Sirupsen/logrus"
)

const (
	AppName = "spore"
)

var (
	ListenAddr  string
	AppConfPath string
	IsProMode   bool

	LogLevel log.Level
	LogFile  string

	AliyunAppKey          string
	AliyunAppSecrety      string
	AliyunRegionId        string
	AliyunZoneId          string
	AliyunSecurityGroupId string
	UsedInstanceTag       map[string]string

	RefreshMinInterval time.Duration
	RefreshMaxInterval time.Duration
)
