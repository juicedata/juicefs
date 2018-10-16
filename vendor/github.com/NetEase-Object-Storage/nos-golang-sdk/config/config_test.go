package config

import (
	. "gopkg.in/check.v1"
	"nos-golang-sdk/logger"
	"nos-golang-sdk/noserror"
	"testing"
)

func Test(t *testing.T) { TestingT(t) }

type ConfigTestSuite struct {
}

var _ = Suite(&ConfigTestSuite{})

func (s *ConfigTestSuite) SetUpSuite(c *C) {
	noserror.Init()
}

func (s *ConfigTestSuite) TestConfig(c *C) {
	config := Config{
		Endpoint:                    "nos.netease.com",
		AccessKey:                   "12345",
		SecretKey:                   "12345",
		NosServiceConnectTimeout:    2,
		NosServiceReadWriteTimeout:  2,
		NosServiceMaxIdleConnection: 2,
		LogLevel:                    logger.LogLevel(logger.DEBUG),
		Logger:                      logger.NewDefaultLogger(),
	}

	err := config.Check()
	c.Assert(err, IsNil)
}

func (s *ConfigTestSuite) TestConfigWithoutLog(c *C) {
	config := Config{
		Endpoint:                    "nos.netease.com",
		AccessKey:                   "12345",
		SecretKey:                   "12345",
		NosServiceConnectTimeout:    2,
		NosServiceReadWriteTimeout:  2,
		NosServiceMaxIdleConnection: 2,
	}

	err := config.Check()
	c.Assert(err, IsNil)
}

func (s *ConfigTestSuite) TestConfigError(c *C) {
	config := Config{
		Endpoint:                    "",
		AccessKey:                   "12345",
		SecretKey:                   "12345",
		NosServiceConnectTimeout:    2,
		NosServiceReadWriteTimeout:  2,
		NosServiceMaxIdleConnection: 2,
	}

	err := config.Check()
	c.Assert(err.Error(), Equals, "StatusCode = 420, Resource = , Message = Config: InvalidEndpoint")
}

func (s *ConfigTestSuite) TestConfigError2(c *C) {
	config := Config{
		Endpoint:                    "nos.netease.com",
		AccessKey:                   "12345",
		SecretKey:                   "12345",
		NosServiceConnectTimeout:    -2,
		NosServiceReadWriteTimeout:  2,
		NosServiceMaxIdleConnection: 2,
	}

	err := config.Check()
	c.Assert(err.Error(), Equals, "StatusCode = 421, Resource = , Message = Config: InvalidConnectionTimeout")
}

func (s *ConfigTestSuite) TestConfigError3(c *C) {
	config := Config{
		Endpoint:                    "nos.netease.com",
		AccessKey:                   "12345",
		SecretKey:                   "12345",
		NosServiceConnectTimeout:    2,
		NosServiceReadWriteTimeout:  -2,
		NosServiceMaxIdleConnection: 2,
	}

	err := config.Check()
	c.Assert(err.Error(), Equals, "StatusCode = 422, Resource = , Message = Config: InvalidReadWriteTimeout")
}

func (s *ConfigTestSuite) TestConfigError4(c *C) {
	config := Config{
		Endpoint:                    "nos.netease.com",
		AccessKey:                   "12345",
		SecretKey:                   "12345",
		NosServiceConnectTimeout:    2,
		NosServiceReadWriteTimeout:  2,
		NosServiceMaxIdleConnection: -2,
	}

	err := config.Check()
	c.Assert(err.Error(), Equals, "StatusCode = 423, Resource = , Message = Config: InvalidMaxIdleConnect")
}
