package ini

import (
	"gopkg.in/ini.v1"
)

type (
	//config interface
	Config interface {
		//read config
		ReadConfig(cfgPath string, config interface{}) error
	}

	//config object
	configImpl struct {
	}
)

//new config
func NewConfig() Config {
	return &configImpl{}
}

//read config
func (c *configImpl) ReadConfig(cfgPath string, config interface{}) error {
	cfg, err := ini.LoadSources(ini.LoadOptions{
		SpaceBeforeInlineComment: true,
	}, cfgPath)
	if err != nil {
		return err
	}
	return cfg.MapTo(config)
}
