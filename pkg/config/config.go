package config

import (
	"crypto/md5"
	"fmt"
	"io"
	"io/ioutil"
	"os"

	"github.com/kubegames/kubegames-operator/pkg/tools"
)

var (
	ConfigPath = tools.MountPath + "/" + tools.MountConfigName
)

type (
	Config interface {

		//load config
		LoadConfig()

		//config
		Config() []byte
	}

	ConfigImpl struct {
		config []byte
	}
)

//new config
func NewConfig() Config {
	return &ConfigImpl{}
}

//load config
func (c *ConfigImpl) LoadConfig() {
	//open file
	file, err := os.Open(ConfigPath)
	if err != nil {
		panic(err.Error())
	}
	defer file.Close()

	//read all
	buff, err := ioutil.ReadAll(file)
	if err != nil {
		panic(err.Error())
	}

	//set config
	c.config = buff
}

//config
func (c *ConfigImpl) Config() []byte {
	return c.config
}

func (c *ConfigImpl) Md5(str string) string {
	w := md5.New()
	io.WriteString(w, str)
	md5str := fmt.Sprintf("%x", w.Sum(nil))
	return md5str
}
