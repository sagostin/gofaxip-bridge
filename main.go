package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/fsnotify/fsnotify"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// FaxLogEntry represents a log entry for fax transmissions
type FaxLogEntry struct {
	Date           time.Time
	Direction      string
	ID             string
	Device         string
	FilePath       string
	Transmission   string
	DstPhoneNumber string
	UnknownField1  string
	UnknownField2  string
	UnknownField3  string
	UnknownField4  string
	UnknownField5  string
	PageCount      int
	Duration       string
	TotalTime      string
	Success        int
	Status         string
	CallerID       string
	SrcPhoneNumber string
	FaxType        string
}

var (
	sentFaxes = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "fax_sent_total",
		Help: "Total number of sent faxes",
	})
	receivedFaxes = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "fax_received_total",
		Help: "Total number of received faxes",
	})
	failedSent = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "fax_failed_sent_total",
		Help: "Total number of failed sent faxes",
	})
	failedRecv = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "fax_failed_recv_total",
		Help: "Total number of failed received faxes",
	})
)

func init() {
	prometheus.MustRegister(receivedFaxes)
	prometheus.MustRegister(sentFaxes)
}

func main() {
	var filePath string
	var spoolerPath string
	flag.StringVar(&filePath, "path", "/var/log/gofaxip/xferfaxlog", "Path to the log file")
	flag.Parse()

	flag.StringVar(&spoolerPath, "spoolerPath", "/var/spool/hylafax", "Path to the spooler directory")
	flag.Parse()

	log.Info("Starting up")

	go func() {
		http.Handle("/metrics", promhttp.Handler())
		log.Fatal(http.ListenAndServe(":9100", nil))
	}()

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		fmt.Println("ERROR:", err)
		return
	}
	defer watcher.Close()

	done := make(chan bool)

	go processFile(filePath, spoolerPath)

	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if event.Op&fsnotify.Write == fsnotify.Write {
					processFile(filePath, spoolerPath)
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				fmt.Println("ERROR:", err)
			}
		}
	}()

	err = watcher.Add(filePath)
	if err != nil {
		fmt.Println("ERROR:", err)
	}
	<-done
}

func processFile(filePath string, spoolerDir string) {
	file, err := os.Open(filePath)
	if err != nil {
		fmt.Println("ERROR:", err)
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		entry, err := parseLogLine(line, spoolerDir)
		if err != nil {
			log.Errorf("ERROR: %s", err)
			continue
		}

		log.Printf("%+v\n", entry)
	}

	if err := scanner.Err(); err != nil {
		log.Errorf("ERROR: %s", err)
	}
}

func parseLogLine(line string, spoolerDir string) (FaxLogEntry, error) {
	//log.Info(line)
	parts := strings.Split(line, "\t")
	if len(parts) < 19 {
		return FaxLogEntry{}, fmt.Errorf("invalid log line: %s", line)
	}

	parts2 := strings.Split(parts[0], " ")

	dateStr := fmt.Sprintf("%s %s", parts2[0], parts2[1])
	date, err := time.Parse("06/01/02 15:04", dateStr)
	if err != nil {
		return FaxLogEntry{}, fmt.Errorf("invalid date format: %v", err)
	}

	pageCount, err := strconv.Atoi(strings.Replace(parts[10], " ", "", -1))
	if err != nil {
		return FaxLogEntry{}, fmt.Errorf("invalid page count: %v", err)
	}

	//log.Info(parts, parts2)

	entry := FaxLogEntry{
		Date:           date,
		Direction:      strings.Replace(parts[1], " ", "", -1),
		ID:             strings.Replace(parts[2], " ", "", -1),
		Device:         strings.Replace(parts[3], " ", "", -1),
		FilePath:       strings.Replace(parts[4], " ", "", -1),
		UnknownField1:  strings.Replace(parts[5], " ", "", -1),
		Transmission:   strings.Replace(parts[6], " ", "", -1),
		DstPhoneNumber: strings.Replace(strings.Replace(parts[7], " ", "", -1), "\"", "", -1),
		UnknownField2:  strings.Replace(parts[8], " ", "", -1),
		PageCount:      pageCount,
		Duration:       strings.Replace(parts[11], " ", "", -1),
		TotalTime:      strings.Replace(parts[12], " ", "", -1),
		Status:         strings.Replace(strings.Replace(parts[13], " ", "", -1), "\"", "", -1),
		CallerID:       strings.Replace(parts[14], "\"", "", -1), // Caller ID is surrounded by "
		SrcPhoneNumber: strings.Replace(strings.Replace(parts[15], " ", "", -1), "\"", "", -1),
		UnknownField4:  strings.Replace(parts[16], " ", "", -1),
		UnknownField5:  strings.Replace(parts[17], " ", "", -1),
		FaxType:        strings.Replace(parts[18], " ", "", -1),
	}

	marshal, _ := json.Marshal(entry)
	log.Info(string(marshal))

	switch entry.Direction {
	case "RECV":
		receivedFaxes.Inc()
		log.Info("Received fax...")
		if entry.Status != "OK" {
			failedRecv.Inc()
			log.Warning("Failed to receive fax...")
			break
		}

		err := sendFax(entry, spoolerDir)
		if err != nil {
			log.Errorf("Failed to send fax: %s", err)
		}

		break
	case "SEND":
		sentFaxes.Inc()
		log.Warning("Sent fax... not processing...")
		if entry.Status != "OK" {
			failedRecv.Inc()
			log.Warning("Failed to bridge fax...")
			break
		}
		break
	default:
		log.Warning("Unknown fax direction...")
		break
	}

	return entry, nil
}

func sendFax(entry FaxLogEntry, spoolDir string) error {
	time.Sleep(2 * time.Second) // wait for fax to be written to disk
	// Example command: sendfax -d destination_number -c caller_id file_path
	log.Info("Sending fax...")
	//log.Warning("/bin/bash", "-c", "sendfax", "-o", entry.SrcPhoneNumber, "-d", entry.DstPhoneNumber, "-c", entry.CallerID, fmt.Sprintf("%s/%s", spoolDir, entry.FilePath))
	// sendfax -n -S 2507620300 -c "TOPS Telecom" -d 2508591501 /var/spool/hylafax/recvq/fax00000343.tif
	cmd := exec.Command("/bin/bash", "-c", "sendfax"+
		" -n -S "+entry.SrcPhoneNumber+
		" -c \""+entry.CallerID+
		"\" -d "+entry.DstPhoneNumber+
		" "+fmt.Sprintf("%s/%s", spoolDir, entry.FilePath))
	_, err := cmd.CombinedOutput()
	//log.Info(string(output))
	if err != nil {
		return fmt.Errorf("sendfax command failed: %w", err)
	}

	return nil
}
