package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/fsnotify/fsnotify"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// LokiClient holds the configuration for the Loki client.
type LokiClient struct {
	PushURL  string // URL to Loki's push API
	Username string // Username for basic auth
	Password string // Password for basic auth
}

// LogEntry represents a single log entry.
type LogEntry struct {
	Timestamp string `json:"timestamp"`
	Line      string `json:"line"`
}

// LokiPushData represents the data structure required by Loki's push API.
type LokiPushData struct {
	Streams []LokiStream `json:"streams"`
}

// LokiStream represents a stream of logs with the same labels in Loki.
type LokiStream struct {
	Stream map[string]string `json:"stream"`
	Values [][2]string       `json:"values"` // Array of [timestamp, line] tuples
}

// NewLokiClient creates a new client to interact with Loki.
func NewLokiClient(pushURL, username, password string) *LokiClient {
	return &LokiClient{
		PushURL:  pushURL,
		Username: username,
		Password: password,
	}
}

// PushLog sends a log entry to Loki.
func (c *LokiClient) PushLog(labels map[string]string, entry LogEntry) error {
	// Prepare the payload
	payload := LokiPushData{
		Streams: []LokiStream{
			{
				Stream: labels,
				Values: [][2]string{{entry.Timestamp, entry.Line}},
			},
		},
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("error marshaling json: %w", err)
	}

	// Create a new request
	req, err := http.NewRequest("POST", c.PushURL, bytes.NewBuffer(jsonPayload))
	if err != nil {
		return fmt.Errorf("error creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// Set basic auth if credentials are provided
	if c.Username != "" && c.Password != "" {
		req.SetBasicAuth(c.Username, c.Password)
	}

	// Send the request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("error sending request to Loki: %w", err)
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {

		}
	}(resp.Body)

	responseBody, _ := ioutil.ReadAll(resp.Body)
	fmt.Println("Loki response:", string(responseBody)) // Print response body for debugging
	marshal, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	log.Warnf("Loki response: %s", string(marshal))

	// Check the response status code
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("received non-200 response status: %d", resp.StatusCode)
	}

	return nil
}

// XFDirection is a custom type to represent the direction of the fax transmission.
type XFDirection string

// Constants for the XFDirection to ensure they can only be "RECV" or "SEND".
const (
	XflRECV XFDirection = "RECV"
	XflSEND XFDirection = "SEND"
)

// XFRecord holds all data for a HylaFAX xferfaxlog record.
type XFRecord struct {
	Ts        time.Time   `json:"ts"`
	Commid    string      `json:"commid,omitempty"`
	Modem     string      `json:"modem,omitempty"`
	Jobid     string      `json:"jobid,omitempty"`
	Jobtag    string      `json:"jobtag,omitempty"`
	Filename  string      `json:"filename,omitempty"`
	Sender    string      `json:"sender,omitempty"`
	Destnum   string      `json:"destnum,omitempty"`
	RemoteID  string      `json:"remoteID,omitempty"`
	Params    string      `json:"params,omitempty"`
	Pages     uint        `json:"pages,omitempty"`
	Jobtime   string      `json:"jobtime,omitempty"`
	Conntime  string      `json:"conntime,omitempty"`
	Reason    string      `json:"reason,omitempty"`
	Cidname   string      `json:"cidname,omitempty"`
	Cidnum    string      `json:"cidnum,omitempty"`
	Owner     string      `json:"owner,omitempty"`
	Dcs       string      `json:"dcs,omitempty"`
	Direction XFDirection `json:"direction,omitempty"`
}

var lokiURL, lokiUser, lokiPass string

var processedFilePath string // New flag for log file path
var fsWatcher *fsnotify.Watcher
var lokiClient *LokiClient

func main() {
	var logFilePath string
	var spoolerPath string
	var logDirPath string // New variable for log directory path

	flag.StringVar(&logFilePath, "path", "/var/log/gofaxip/xferfaxlog", "Path to the log file")
	flag.StringVar(&spoolerPath, "spoolerPath", "/var/spool/hylafax", "Path to the spooler directory")
	flag.StringVar(&logDirPath, "logDir", "./log", "Path to the log directory") // New flag for log directory

	flag.StringVar(&lokiURL, "lokiURL", "", "URL to Loki's push API")
	flag.StringVar(&lokiUser, "lokiUser", "", "Username for Loki")
	flag.StringVar(&lokiPass, "lokiPass", "", "Password for Loki")

	flag.Parse()

	if lokiURL != "" {
		lokiClient = NewLokiClient(lokiURL, lokiUser, lokiPass)
	}

	// Ensure log directory exists
	if err := os.MkdirAll(logDirPath, os.ModePerm); err != nil {
		log.Fatalf("Failed to create log directory: %s", err)
	}
	processedFilePath = filepath.Join(logDirPath, "processed_faxes.log") // Set the processed file path

	log.Info("Starting up")

	go func() {
		http.Handle("/metrics", promhttp.Handler())
		log.Fatal(http.ListenAndServe(":9100", nil))
	}()
	// Create a new watcher
	watcher, err := fsnotify.NewWatcher()
	fsWatcher = watcher
	if err != nil {
		log.Errorf("ERROR creating watcher: %s", err)
		return
	}
	defer func() {
		err := fsWatcher.Close()
		if err != nil {
			log.Errorf("Error closing watcher: %s", err)
		}
	}()

	// Function to safely re-add the file to the watcher
	reAddFileToWatcher := func() {
		time.Sleep(100 * time.Millisecond) // Short delay to ensure file exists
		if err := watcher.Add(logFilePath); err != nil {
			log.Errorf("ERROR re-adding file to watcher: %s", err)
		}
	}

	// Add the file to the watcher initially
	reAddFileToWatcher()

	// Process file initially
	go processFile(logFilePath, spoolerPath)

	// Watcher and polling loop
	go func() {
		for {
			select {
			case event := <-watcher.Events:
				if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename|fsnotify.Remove|fsnotify.Chmod) != 0 {
					processFile(logFilePath, spoolerPath)
					if event.Op&(fsnotify.Rename|fsnotify.Remove) != 0 {
						reAddFileToWatcher()
					}
				}
			case err := <-watcher.Errors:
				log.Errorf("Watcher error: %s", err)
				reAddFileToWatcher() // Attempt to recover from watcher error
			case <-time.After(10 * time.Second): // Polling interval
				processFile(logFilePath, spoolerPath) // Periodic recheck
			}
		}
	}()

	// Block forever
	select {}
}

// processFile processes the log file, skipping already processed lines
func processFile(filePath string, spoolerDir string) {
	processedLines, err := readLines(processedFilePath)
	if err != nil {
		log.Errorf("Error reading processed lines log: %s", err)
		return
	}
	processedLinesSet := make(map[string]struct{})
	for _, line := range processedLines {
		processedLinesSet[line] = struct{}{}
	}

	file, err := os.Open(filePath)
	if err != nil {
		log.Errorf("Error opening log file: %s", err)
		return
	}
	defer func(file *os.File) {
		err := file.Close()
		if err != nil {

		}
	}(file)

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if _, processed := processedLinesSet[line]; processed {
			continue // Skip already processed lines
		}

		entry, err := parseLogLine(line, spoolerDir, filePath)
		if err != nil {
			log.Errorf("ERROR: %s", err)
			continue
		}

		log.Printf("%+v\n", entry)

		err = appendToLogFile(line) // Append the processed line to the log
		if err != nil {
			log.Errorf("Error appending to processed lines log: %s", err)
		}

		// If Loki client is configured, send the log entry to Loki
		if lokiClient != nil {
			jsonData, err := json.Marshal(entry)
			if err != nil {
				log.Errorf("Failed to marshal log entry: %v", err)
				continue
			}

			labels := map[string]string{"job": "xferfaxlog", "instance": "faxrelay"}
			logEntry := LogEntry{
				Timestamp: strconv.FormatInt(time.Now().UnixNano(), 10),
				Line:      string(jsonData),
			}
			err = lokiClient.PushLog(labels, logEntry)
			if err != nil {
				fmt.Printf("Failed to push log to Loki: %v\n", err)
			} else {
				fmt.Println("Log pushed to Loki successfully.")
			}
		}
	}

	if err := scanner.Err(); err != nil {
		log.Errorf("Scanner error: %s", err)
	}
}

// appendToLogFile appends a line to the processed faxes log file
func appendToLogFile(line string) error {
	f, err := os.OpenFile(processedFilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer func(f *os.File) {
		err := f.Close()
		if err != nil {

		}
	}(f)

	_, err = f.WriteString(line + "\n")
	return err
}

// readLines reads all lines from a file and returns them as a slice of strings
func readLines(filePath string) ([]string, error) {
	file, err := os.OpenFile(filePath, os.O_RDONLY|os.O_CREATE, 0644)
	if err != nil {
		return nil, err
	}
	defer func(file *os.File) {
		err := file.Close()
		if err != nil {

		}
	}(file)

	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines, scanner.Err()
}

var recvPattern = `(?P<Date>\d{2}\/\d{2}\/\d{2} \d{2}:\d{2})\s+(?P<Direction>RECV)\s+(?P<CommID>\w+)\s+(?P<Modem>\w+)\s+(?P<Filename>\S+)\s+""\s+fax\s+"(?P<DestPhoneNumber>\d+)"\s+"(?P<RemoteID>[^"]*)"\s+(?P<Params>\d+)\t+(?P<Pages>\d+)\t(?P<JobTime>\d+:\d{2}:\d{2})\s+(?P<ConnTime>\d+:\d{2}:\d{2})\t"(?P<Reason>[^"]*)"\s+""(?P<CIDName>[^"]*)""\s+""(?P<CIDNumber>[^"]*)""\s+""+\s+""+\s+"(?P<Dcs>[^"]*)"`
var sendPattern = `(?P<Date>\d{2}\/\d{2}\/\d{2} \d{2}:\d{2})\s+(?P<Direction>SEND)\s+(?P<CommID>\w+)\s+(?P<Modem>\w+)\s+(?P<JobID>\S+)\s+"(?P<JobTag>[^"]*)"\s+(?P<Sender>\S+)\s+"(?P<DestPhoneNumber>\d+)"\s+"(?P<RemoteID>[^"]*)"\s+(?P<Params>\d+)\t+(?P<Pages>\d+)\t(?P<JobTime>\d+:\d{2}:\d{2})\s+(?P<ConnTime>\d+:\d{2}:\d{2})\t"(?P<Reason>[^"]*)"\s+""\s+""\s+""\s+"(?P<CIDNumber>[^"]*)"\s+"(?P<Dcs>[^"]*)"`

func parseLogLine(line string, spoolerDir string, logFilePath string) (XFRecord, error) {
	//log.Info(line)
	var logPattern string

	entry := XFRecord{
		Ts:        time.Time{},
		Commid:    "",
		Modem:     "",
		Jobid:     "",
		Jobtag:    "",
		Filename:  "",
		Sender:    "",
		Destnum:   "",
		RemoteID:  "",
		Params:    "",
		Pages:     0,
		Jobtime:   "",
		Conntime:  "",
		Reason:    "",
		Cidname:   "",
		Cidnum:    "",
		Owner:     "",
		Dcs:       "",
		Direction: "",
	}

	if strings.Contains(line, "RECV") {
		entry.Direction = XflRECV
		logPattern = recvPattern
	} else if strings.Contains(line, "SEND") {
		entry.Direction = XflSEND
		logPattern = sendPattern
	} else {
		return XFRecord{}, fmt.Errorf("invalid fax direction")
	}

	r := regexp.MustCompile(logPattern)
	match := r.FindStringSubmatch(line)

	if match == nil {
		return XFRecord{}, fmt.Errorf("invalid log line format")
	}

	ts, err := time.Parse("01/02/06 15:04", match[r.SubexpIndex("Date")])
	if err != nil {
		return XFRecord{}, fmt.Errorf("invalid date format: %v", err)
	}

	pageCount, err := strconv.Atoi(match[r.SubexpIndex("Pages")])
	if err != nil {
		return XFRecord{}, fmt.Errorf("invalid page count: %v", err)
	}

	/*jobTime, err := time.ParseDuration(match[r.SubexpIndex("JobTime")])
	if err != nil {
		return XFRecord{}, fmt.Errorf("invalid duration format: %v", err)
	}

	connTime, err := time.ParseDuration(match[r.SubexpIndex("ConnTime")])
	if err != nil {
		return XFRecord{}, fmt.Errorf("invalid duration format: %v", err)
	}*/

	entry.Ts = ts
	entry.Pages = uint(pageCount)
	/*	entry.Jobtime = jobTime
		entry.Conntime = connTime*/
	entry.Destnum = match[r.SubexpIndex("DestPhoneNumber")]
	entry.Commid = match[r.SubexpIndex("CommID")]
	entry.Modem = match[r.SubexpIndex("Modem")]
	entry.RemoteID = match[r.SubexpIndex("RemoteID")]
	entry.Reason = match[r.SubexpIndex("Reason")]

	entry.Jobtime = match[r.SubexpIndex("JobTime")]
	entry.Conntime = match[r.SubexpIndex("ConnTime")]

	if strings.Contains(line, "RECV") {
		entry.Filename = match[r.SubexpIndex("Filename")]
		entry.Cidnum = match[r.SubexpIndex("CIDNumber")]
		entry.Cidname = match[r.SubexpIndex("CIDName")]
	} else if strings.Contains(line, "SEND") {
		entry.Direction = XflSEND
		entry.Jobtag = match[r.SubexpIndex("JobTag")]
		entry.Jobid = match[r.SubexpIndex("JobID")]
		entry.Sender = match[r.SubexpIndex("Sender")]
	} else {
		log.Warn("Unknown fax direction...")
	}

	entry.Dcs = match[r.SubexpIndex("Dcs")]

	marshal, _ := json.Marshal(entry)
	log.Info(string(marshal))

	switch entry.Direction {
	case "RECV":
		//receivedFaxes.Inc()
		log.Info("Received fax...")
		if entry.Reason != "OK" {
			//failedRecv.Inc()
			log.Warning("Failed to receive fax...")
			break
		} else {
			err := sendFax(entry, spoolerDir)
			if err != nil {
				log.Errorf("Failed to send fax: %s", err)
			}
		}
		break
	case "SEND":
		//sentFaxes.Inc()
		log.Warning("Sent fax... not processing...")
		if entry.Reason != "OK" {
			//failedRecv.Inc()
			log.Warning("Failed to bridge fax...")
			break
		}
		break
	default:
		log.Warning("Unknown fax direction...")
		break
	}

	// Create a temporary file for writing
	tempFile, err := ioutil.TempFile("", "temp")
	if err != nil {
		log.Errorf("ERROR: %s", err)
		return XFRecord{}, err // Make sure to return here to avoid nil pointer dereference
	}

	// Set file permissions to be readable and writable by all users
	if err := tempFile.Chmod(0666); err != nil {
		log.Errorf("ERROR: %s", err)
		return XFRecord{}, err
	}

	// Read the file line by line
	lines, err := readLines(logFilePath)
	if err != nil {
		log.Errorf("ERROR: %s", err)
		return XFRecord{}, err
	}

	/*var removedLine string*/
	for _, line1 := range lines {
		if line1 == line {
			//removedLine = line1
			continue // Skip the line to remove
		}
		_, err := tempFile.WriteString(line1 + "\n") // It should be line1 here, not line
		if err != nil {
			log.Errorf("ERROR: %s", err)
			return XFRecord{}, err
		}
	}

	err = tempFile.Close()
	if err != nil {
		log.Errorf("ERROR: %s", err)
	}

	/*// Write the removed line to the log file
	err = appendToLogFile(removedLine)
	if err != nil {
		log.Errorf("ERROR: %s", err)
		return XFRecord{}, err
	}*/

	// Replace the original file with the temporary file
	err = os.Rename(tempFile.Name(), logFilePath)
	if err != nil {
		log.Errorf("ERROR: %s", err)
		return XFRecord{}, err
	}

	err = fsWatcher.Add(logFilePath)
	if err != nil {
		return XFRecord{}, err
	}

	return entry, nil
}

func sendFax(entry XFRecord, spoolDir string) error {
	time.Sleep(2 * time.Second) // wait for fax to be written to disk
	// Example command: sendfax -d destination_number -c caller_id file_path
	log.Info("Sending fax...")
	//log.Warning("/bin/bash", "-c", "sendfax", "-o", entry.SrcPhoneNumber, "-d", entry.DstPhoneNumber, "-c", entry.CallerID, fmt.Sprintf("%s/%s", spoolDir, entry.FilePath))
	// sendfax -n -S 2507620300 -c "TOPS Telecom" -d 2508591501 /var/spool/hylafax/recvq/fax00000343.tif
	cmd := exec.Command("/bin/bash", "-c", "sendfax"+
		" -n -S "+entry.Cidnum+
		" -o "+entry.Cidnum+
		" -c \""+entry.Cidname+
		"\" -d "+entry.Destnum+
		" "+fmt.Sprintf("%s/%s", spoolDir, entry.Filename))
	_, err := cmd.CombinedOutput()
	//log.Info(string(output))
	if err != nil {
		return fmt.Errorf("sendfax command failed: %w", err)
	}

	// Delete the fax file after sending
	err = os.Remove(fmt.Sprintf("%s/%s", spoolDir, entry.Filename))
	if err != nil {
		log.Errorf("Failed to delete fax file: %s", err)
		return err
	}

	log.Info("Fax file deleted successfully")

	return nil
}
