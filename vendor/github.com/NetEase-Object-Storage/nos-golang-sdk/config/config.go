package config

import (
	"github.com/NetEase-Object-Storage/nos-golang-sdk/logger"
	"github.com/NetEase-Object-Storage/nos-golang-sdk/noserror"
	"github.com/NetEase-Object-Storage/nos-golang-sdk/utils"
)

type Config struct {
	Endpoint      string
	AccessKey     string
	SecretKey     string

	NosServiceConnectTimeout    int
	NosServiceReadWriteTimeout  int
	NosServiceMaxIdleConnection int

	LogLevel *logger.LogLevelType

	Logger logger.Logger
}

func (conf *Config) Check() error {
	if conf.Endpoint == "" {
		return utils.ProcessClientError(noserror.ERROR_CODE_CFG_ENDPOINT, "", "", "")
	}

	if conf.NosServiceConnectTimeout < 0 {
		return utils.ProcessClientError(noserror.ERROR_CODE_CFG_CONNECT_TIMEOUT, "", "", "")
	}

	if conf.NosServiceReadWriteTimeout < 0 {
		return utils.ProcessClientError(noserror.ERROR_CODE_CFG_READWRITE_TIMEOUT, "", "", "")
	}

	if conf.NosServiceMaxIdleConnection < 0 {
		return utils.ProcessClientError(noserror.ERROR_CODE_CFG_MAXIDLECONNECT, "", "", "")
	}

	if conf.NosServiceConnectTimeout == 0 {
		conf.NosServiceConnectTimeout = 30
	}

	if conf.NosServiceReadWriteTimeout == 0 {
		conf.NosServiceReadWriteTimeout = 60
	}

	if conf.NosServiceMaxIdleConnection == 0 {
		conf.NosServiceMaxIdleConnection = 60
	}

	if conf.Logger == nil {
		conf.Logger = logger.NewDefaultLogger()
	}

	if conf.LogLevel == nil {
		conf.LogLevel = logger.LogLevel(logger.DEBUG)
	}

	return nil
}
