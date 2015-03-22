package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/coreos/go-systemd/dbus"
	"github.com/coreos/go-systemd/unit"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

var (
	unitPath = "/etc/systemd/system"
	hostname string
	apiKey   string
	apiUrl   string
)

const (
	SUCC = 0
	WARN = 1
	CRIT = 2
)

type Status struct {
	Code int
	Desc string
	Unit string
	Host string
	Id   string
}

type DatadogStatus struct {
	Check    string   `json:"check"`
	HostName string   `json:"host_name"`
	Status   int      `json:"status"`
	Message  string   `json:"message"`
	Tags     []string `json:"tags"`
}

func (self Status) String() string {
	return fmt.Sprintf(
		"id=\"%s\" unit=\"%s\" code=%d message=\"%s\" host=\"%s\"",
		self.Id,
		self.Unit,
		self.Code,
		self.Desc,
		self.Host,
	)
}

func NewDatadogStatus(s Status) DatadogStatus {
	tags := []string{}
	tags = append(tags, fmt.Sprintf("check:%s", s.Unit))
	return DatadogStatus{
		Check:    s.Id,
		HostName: s.Host,
		Status:   s.Code,
		Message:  s.Desc,
		Tags:     tags,
	}
}

type Check interface {
	Get(string) Status
}

type CheckSystemdStatus struct {
	Check
}

func (self CheckSystemdStatus) Get(name string) Status {
	newStatus := Status{
		Unit: name,
		Host: hostname,
		Id:   "systemd.unit.check_status",
	}

	conn, err := dbus.New()
	if err != nil {
		newStatus.Code = CRIT
		newStatus.Desc = fmt.Sprintf("dbus.New() - %s", err)
		return newStatus
	}

	defer conn.Close()

	units, err := conn.ListUnits()
	if err != nil {
		newStatus.Code = CRIT
		newStatus.Desc = fmt.Sprintf("dbus.ListUnits() - %s", err)
		return newStatus
	}

	for _, unit := range units {
		if unit.Name == name {
			if unit.ActiveState == "failed" {
				newStatus.Code = CRIT
				newStatus.Desc = fmt.Sprintf("Unit %s in failed state", name)
				return newStatus
			}
			if unit.ActiveState != "active" {
				newStatus.Code = WARN
				newStatus.Desc = fmt.Sprintf("Unit %s in %s state", name, unit.ActiveState)
				return newStatus
			}
			if unit.LoadState != "loaded" {
				newStatus.Code = WARN
				newStatus.Desc = fmt.Sprintf("Unit %s in %s state", name, unit.LoadState)
				return newStatus
			}

			newStatus.Code = SUCC
			newStatus.Desc = fmt.Sprintf("Unit %s in active state", name)
			return newStatus
		}
	}

	newStatus.Code = CRIT
	newStatus.Desc = fmt.Sprintf("Unit %s not found", name)

	return newStatus
}

type Unit struct {
	Name   string
	Desc   string
	Checks []Check
}

type Checker struct {
	Units []Unit
	done  chan bool
}

func NewChecker(path string) (*Checker, error) {
	mask := fmt.Sprintf("%s/*.service", path)
	log.Printf("Search services by %s", mask)
	files, err := filepath.Glob(mask)
	if err != nil {
		return nil, err
	}

	var units []Unit

	for _, filename := range files {
		content, err := ioutil.ReadFile(filename)
		if err != nil {
			return nil, err
		}

		sections, err := unit.Deserialize(bytes.NewReader(content))
		if err != nil {
			return nil, err
		}

		newUnit := Unit{
			Name: filepath.Base(filename),
		}

		checks := []Check{}

		for _, section := range sections {
			if section.Section == "Unit" && section.Name == "Description" {
				newUnit.Desc = section.Value
			}

			if section.Section == "X-Check" && section.Name == "Systemd" && section.Value == "status" {
				newCheck := CheckSystemdStatus{}
				checks = append(checks, newCheck)
			}
		}

		if len(checks) > 0 {
			newUnit.Checks = checks
			units = append(units, newUnit)
		}
	}

	checker := &Checker{
		Units: units,
		done:  make(chan bool),
	}

	return checker, nil
}

func (self *Checker) Run() []Status {

	newStatuses := []Status{}

	for _, unit := range self.Units {
		for _, check := range unit.Checks {
			newStatuses = append(newStatuses, check.Get(unit.Name))
		}
	}

	for _, st := range newStatuses {
		log.Printf("Check %s", st)
	}

	if len(newStatuses) > 0 {
		self.Notify(newStatuses)
	}

	return newStatuses
}

func (self *Checker) Stop() {
	close(self.done)
}

func (self *Checker) Watch() {
	self.Run()

	for {
		select {
		case <-time.After(30 * time.Second):
			self.Run()
		case <-self.done:
			log.Printf("Shutdown complete")
			return
		}
	}
}

func (self *Checker) Notify(statuses []Status) {
	if apiUrl == "" {
		return
	}

	log.Printf("Sending to datadog")

	for _, status := range statuses {
		ddStatus := NewDatadogStatus(status)

		payload, err := json.Marshal(ddStatus)
		if err != nil {
			log.Printf("json.Marshal failed %+v", err)
		}

		req, err := http.NewRequest("POST", apiUrl, bytes.NewBuffer(payload))
		req.Header.Set("Content-Type", "application/json")

		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			log.Printf("Datadog request failed %s - %+v", err, resp)
		}
		defer resp.Body.Close()
	}

	log.Printf("Datadog requests complete")
}

func installSignalHandler(c *Checker) {
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc,
		syscall.SIGHUP,
		syscall.SIGINT,
		syscall.SIGTERM,
		syscall.SIGQUIT)
	go func() {
		s := <-sigc
		log.Printf("Got signal: %s", s)
		c.Stop()
	}()
}

func init() {
	var err error

	log.SetFlags(0)

	hostname, err = os.Hostname()
	if err != nil {
		hostname = "localhost"
	}

	apiKey = os.Getenv("DATADOG_API_KEY")
	if apiKey != "" {
		apiUrl = fmt.Sprintf("https://app.datadoghq.com/api/v1/check_run?api_key=%s", apiKey)
	}
}

func main() {
	conn, err := dbus.New()
	if err != nil {
		panic(err)
	}
	defer conn.Close()

	checker, err := NewChecker("system")
	if err != nil {
		panic(err)
	}

	for _, unit := range checker.Units {
		log.Printf("Add unit \"%s\"", unit.Name)
	}

	installSignalHandler(checker)

	checker.Watch()
}
