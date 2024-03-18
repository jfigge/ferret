package internal

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Configuration struct {
	Hosts   []*Host   `yaml:"hosts"`
	Tunnels []*Tunnel `yaml:"tunnels"`
}

func (c *Configuration) Load(configFile string) *Configuration {
	if fi, err := os.Stat(configFile); os.IsNotExist(err) {
		fmt.Printf("  Error - config file (%s) cannot be read: file not found\n", configFile)
		return nil
	} else if fi.IsDir() {
		fmt.Printf("  Error - config file (%s) cannot be read: file is a directory\n", configFile)
		return nil
	}
	bs, err := os.ReadFile(configFile)
	if err != nil {
		if os.IsPermission(err) {
			fmt.Printf("  Error - config file (%s) cannot be read: permission denied\n", configFile)
		} else {
			fmt.Printf("  Error - config file (%s) cannot be read: %v\n", configFile, err)
		}
		return nil
	}

	config := Configuration{}
	if strings.HasSuffix(configFile, "yaml") || strings.HasSuffix(configFile, "yml") {
		err = yaml.Unmarshal(bs, &config)
	} else if strings.HasSuffix(configFile, "json") {
		err = json.Unmarshal(bs, &config)
	} else {
		fmt.Printf("  Error - config file (%s) has unknown extension\n", configFile)
		return nil
	}
	if err != nil {
		fmt.Printf("  Error - config file (%s) cannot be parsed: %v\n", configFile, err)
		return nil
	}
	return &config
}

func (c *Configuration) Validate(defaultUsername string) bool {
	valid := true
	for _, host := range c.Hosts {
		if !host.Validate(defaultUsername) {
			valid = false
		}
	}
	for _, tunnel := range c.Tunnels {
		if !tunnel.Validate() {
			valid = false
		}
	}
	if !validateJumpHosts() {
		valid = false
	}
	var unused []string
	for name, host := range Hosts {
		if !host.isHost && !host.isJumpHost {
			fmt.Printf("  Info  - host(%s) is unused\n", name)
			unused = append(unused, name)
		}
	}
	for _, name := range unused {
		delete(Hosts, name)
	}
	return valid
}
