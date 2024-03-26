package internal

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

type Address struct {
	valid   bool
	address string
	port    int
}

func NewAddress(address string) *Address {
	return &Address{
		address: address,
	}
}

func (a *Address) ValidateAddress(group string, name string, attr string, remote bool) bool {
	a.valid = true
	parts := strings.Split(a.address, ":")
	if len(parts) != 2 {
		fmt.Printf(
			"  Error - %s(%s) %s(%s) is invalid.  Required syntax is <ip address>:<port>\n",
			group, name, attr, a.address,
		)
		a.valid = false
		return false
	}

	ips, err := net.LookupIP(parts[0])
	if err != nil {
		if !remote {
			fmt.Printf("  Error - %s(%s) %s(%s) cannot be resolved\n", group, name, attr, parts[0])
			a.valid = false
		} else {
			fmt.Printf("  Warn  - %s(%s) %s(%s) cannot be resolved local\n", group, name, attr, parts[0])
		}
	} else if len(ips) == 0 {
		fmt.Printf(
			"  Error - %s(%s) %s(%s) has no valid IP addresses associated with it\n",
			group, name, attr, parts[0],
		)
		a.valid = false
	} else {
		if ipv4 := ips[0].To4(); ipv4 == nil {
			fmt.Printf(
				"  Error - %s(%s) %s(%s) cannot be converted to a valid IP4 address\n",
				group, name, attr, parts[0],
			)
		} else if !remote {
			a.address = ipv4.String()
		} else {
			a.address = parts[0]
		}
	}

	if i, err := strconv.Atoi(parts[1]); err != nil {
		fmt.Printf("  Error - %s(%s) %s port(%s) %v\n", group, name, attr, parts[1], err.Error())
		a.valid = false
	} else if i < 1 || i > 65536 {
		fmt.Printf("  Error - %s(%s) %s port(%s) range is invalid.  Must be between 1 and 65536\n", group, name, attr, parts[1])
		a.valid = false
	} else {
		a.address = fmt.Sprintf("%s:%d", a.address, i)
		a.port = i
	}
	return a.valid
}

func (a *Address) UnmarshalJSON(data []byte) error {
	a.address = strings.TrimSpace(string(data))
	return nil
}

func (a *Address) UnmarshalYAML(unmarshal func(interface{}) error) error {
	return unmarshal(&a.address)
}

func (a *Address) IsBlank() bool {
	return a.address == ""
}

func (a *Address) IsValid() bool {
	return a.valid
}

func (a *Address) Port() interface{} {
	return a.port
}
