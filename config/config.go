/*************************************************************************
 * Copyright 2017 Gravwell, Inc. All rights reserved.
 * Contact: <legal@gravwell.io>
 *
 * This software may be modified and distributed under the terms of the
 * BSD 2-clause license. See the LICENSE file for details.
 **************************************************************************/

package config

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/go-write"
	"github.com/google/uuid"
)

const (
	kb = 1024
	mb = 1024 * kb
	gb = 1024 * mb

	defaultMaxCache = 512
	defaultLogLevel = `ERROR`
	minThrottle     = (1024 * 1024) / 8
)

const (
	envSecret      string = `GRAVWELL_INGEST_SECRET`
	envLogLevel    string = `GRAVWELL_LOG_LEVEL`
	envClearTarget string = `GRAVWELL_CLEARTEXT_TARGETS`
	envEncTarget   string = `GRAVWELL_ENCRYPTED_TARGETS`
	envPipeTarget  string = `GRAVWELL_PIPE_TARGETS`

	DefaultCleartextPort uint16 = 4023
	DefaultTLSPort       uint16 = 4024

	commentValue = `#`
	globalHeader = `[global]`
	headerStart  = `[`
	uuidParam    = `Ingester-UUID`
)

var (
	ErrNoConnections              = errors.New("No connections specified")
	ErrMissingIngestSecret        = errors.New("Ingest-Secret value missing")
	ErrInvalidLogLevel            = errors.New("Invalid Log Level")
	ErrInvalidConnectionTimeout   = errors.New("Invalid connection timeout")
	ErrInvalidIngestCacheSize     = errors.New("Invalid Max Ingest Cache size")
	ErrCacheEnabledZeroMax        = errors.New("Ingest cache enabled with zero Max Cache size")
	ErrGlobalSectionNotFound      = errors.New("Global config section not found")
	ErrInvalidLineLocation        = errors.New("Invalid line location")
	ErrInvalidUpdateLineParameter = errors.New("Update line location does not contain the specified paramter")
)

type IngestConfig struct {
	Ingest_Secret              string
	Connection_Timeout         string
	Verify_Remote_Certificates bool //legacy, will be removed
	Insecure_Skip_TLS_Verify   bool
	Cleartext_Backend_Target   []string
	Encrypted_Backend_Target   []string
	Pipe_Backend_Target        []string
	Ingest_Cache_Path          string
	Max_Ingest_Cache           int64 //maximum amount of data to cache in MB
	Log_Level                  string
	Log_File                   string
	Source_Override            string // override normal source if desired
	Rate_Limit                 string
	Ingester_UUID              string
}

func (ic *IngestConfig) loadDefaults() error {
	//arrange the logic to be secure by default or when there is ambiguity
	if ic.Verify_Remote_Certificates {
		ic.Insecure_Skip_TLS_Verify = false
	}
	//Ingest secret
	if err := LoadEnvVar(&ic.Ingest_Secret, envSecret, ``); err != nil {
		return err
	}
	//Log level
	if err := LoadEnvVar(&ic.Log_Level, envLogLevel, defaultLogLevel); err != nil {
		return err
	}
	//Cleartext targets
	if err := LoadEnvVarList(&ic.Cleartext_Backend_Target, envClearTarget); err != nil {
		return err
	}
	//Encrypted targets
	if err := LoadEnvVarList(&ic.Encrypted_Backend_Target, envEncTarget); err != nil {
		return err
	}
	//Pipe targets
	if err := LoadEnvVarList(&ic.Pipe_Backend_Target, envPipeTarget); err != nil {
		return err
	}
	return nil
}

func (ic *IngestConfig) Verify() error {
	if err := ic.loadDefaults(); err != nil {
		return err
	}

	if ic.Ingester_UUID != `` {
		if _, err := uuid.Parse(ic.Ingester_UUID); err != nil {
			return fmt.Errorf("Malformed ingester UUID %v: %v", ic.Ingester_UUID, err)
		}
	}

	ic.Log_Level = strings.ToUpper(strings.TrimSpace(ic.Log_Level))
	if ic.Max_Ingest_Cache == 0 && len(ic.Ingest_Cache_Path) != 0 {
		ic.Max_Ingest_Cache = defaultMaxCache
	}
	if to, err := ic.parseTimeout(); err != nil || to < 0 {
		if err != nil {
			return err
		}
		return ErrInvalidConnectionTimeout
	}
	if len(ic.Ingest_Secret) == 0 {
		return ErrMissingIngestSecret
	}
	//ensure there is at least one target
	if (len(ic.Cleartext_Backend_Target) + len(ic.Encrypted_Backend_Target) + len(ic.Pipe_Backend_Target)) == 0 {
		return ErrNoConnections
	}

	//normalize the log level and check it
	if err := ic.checkLogLevel(); err != nil {
		return err
	}

	// Make sure the log directory exists.
	logdir := filepath.Dir(ic.Log_File)
	fi, err := os.Stat(logdir)
	if err != nil {
		if os.IsNotExist(err) {
			//try to make the directory
			err = os.MkdirAll(logdir, 0700)
			if err != nil {
				return err
			}
		} else {
			return err
		}
	} else if !fi.IsDir() {
		return errors.New("Log Location is not a directory")
	}

	// Make sure the cache directory exists.
	cachedir := filepath.Dir(ic.Ingest_Cache_Path)
	fi, err = os.Stat(cachedir)
	if err != nil {
		if os.IsNotExist(err) {
			//try to make the directory
			err = os.MkdirAll(cachedir, 0700)
			if err != nil {
				return err
			}
		} else {
			return err
		}
	} else if !fi.IsDir() {
		return errors.New("Cache Location is not a directory")
	}

	//check the max cache
	if ic.Max_Ingest_Cache < 0 {
		return ErrInvalidIngestCacheSize
	} else if ic.Max_Ingest_Cache == 0 && len(ic.Ingest_Cache_Path) != 0 {
		return ErrCacheEnabledZeroMax
	}

	if ic.Source_Override != `` {
		if net.ParseIP(ic.Source_Override) == nil {
			return errors.New("Failed to parse Source_Override")
		}
	}
	return nil
}

func (ic *IngestConfig) Targets() ([]string, error) {
	var conns []string
	for _, v := range ic.Cleartext_Backend_Target {
		conns = append(conns, "tcp://"+AppendDefaultPort(v, DefaultCleartextPort))
	}
	for _, v := range ic.Encrypted_Backend_Target {
		conns = append(conns, "tls://"+AppendDefaultPort(v, DefaultTLSPort))
	}
	for _, v := range ic.Pipe_Backend_Target {
		conns = append(conns, "pipe://"+v)
	}
	if len(conns) == 0 {
		return nil, ErrNoConnections
	}
	return conns, nil
}

func (ic *IngestConfig) InsecureSkipTLSVerification() bool {
	return ic.Insecure_Skip_TLS_Verify
}

func (ic *IngestConfig) Timeout() time.Duration {
	if tos, _ := ic.parseTimeout(); tos > 0 {
		return tos
	}
	return 0
}

func (ic *IngestConfig) Secret() string {
	return ic.Ingest_Secret
}

// EnableCache indicates whether a file cache is enabled
func (ic *IngestConfig) EnableCache() bool {
	return len(ic.Ingest_Cache_Path) != 0
}

// LocalFileCachePath returns the path to the local ingest cache
// an empty string means no cache enabled
func (ic *IngestConfig) LocalFileCachePath() string {
	return ic.Ingest_Cache_Path
}

// MaxCachedData returns the maximum amount of data to be cached in bytes
func (ic *IngestConfig) MaxCachedData() uint64 {
	return uint64(ic.Max_Ingest_Cache * mb)
}

// Return the specified log level
func (ic *IngestConfig) LogLevel() string {
	return ic.Log_Level
}

func (ic *IngestConfig) checkLogLevel() error {
	if len(ic.Log_Level) == 0 {
		ic.Log_Level = defaultLogLevel
		return nil
	}
	switch ic.Log_Level {
	case `OFF`:
		fallthrough
	case `INFO`:
		fallthrough
	case `WARN`:
		fallthrough
	case `ERROR`:
		return nil
	}
	return ErrInvalidLogLevel
}

func (ic *IngestConfig) parseTimeout() (time.Duration, error) {
	tos := strings.TrimSpace(ic.Connection_Timeout)
	if len(tos) == 0 {
		return 0, nil
	}
	return time.ParseDuration(tos)
}

func (ic *IngestConfig) RateLimit() (bps int64, err error) {
	if ic.Rate_Limit == `` {
		return
	}
	bps, err = parseRate(ic.Rate_Limit)
	return
}

//returns whether the supplied uuid is all zeros
func zeroUUID(id uuid.UUID) bool {
	for _, v := range id {
		if v != 0 {
			return false
		}
	}
	return true
}

func (ic *IngestConfig) IngesterUUID() (id uuid.UUID, ok bool) {
	if ic.Ingester_UUID == `` {
		return
	}
	var err error
	if id, err = uuid.Parse(ic.Ingester_UUID); err == nil {
		ok = true
	}
	if zeroUUID(id) {
		ok = false
	}
	return
}

func reloadContent(loc string) (content string, err error) {
	if loc == `` {
		err = errors.New("not loaded from file")
		return
	}
	var bts []byte
	bts, err = ioutil.ReadFile(loc)
	content = string(bts)
	return
}

func (ic *IngestConfig) SetIngesterUUID(id uuid.UUID, loc string) (err error) {
	if zeroUUID(id) {
		return errors.New("UUID is empty")
	}
	var content string
	if content, err = reloadContent(loc); err != nil {
		return
	}
	//crack the config file into lines
	lines := strings.Split(content, "\n")
	lo := argInGlobalLines(lines, uuidParam)
	if lo == -1 {
		//UUID value not set, insert immediately after global
		gStart, _, ok := globalLineBoundary(lines)
		if !ok {
			err = ErrGlobalSectionNotFound
			return
		}
		lines, err = insertLine(lines, fmt.Sprintf(`%s="%s"`, uuidParam, id.String()), gStart+1)
	} else {
		//found it, update it
		lines, err = updateLine(lines, uuidParam, fmt.Sprintf(`"%s"`, id), lo)
	}
	if err != nil {
		return
	}
	ic.Ingester_UUID = id.String()
	content = strings.Join(lines, "\n")
	err = updateConfigFile(loc, content)
	return
}

func updateConfigFile(loc string, content string) error {
	if loc == `` {
		return errors.New("Configuration was loaded with bytes, cannot update")
	}
	fout, err := write.TempFile(filepath.Dir(loc), loc)
	if err != nil {
		return err
	}
	if err := writeFull(fout, []byte(content)); err != nil {
		return err
	}
	return fout.CloseAtomicallyReplace()
}

func writeFull(w io.Writer, b []byte) error {
	var written int
	for written < len(b) {
		if n, err := w.Write(b[written:]); err != nil {
			return err
		} else if n == 0 {
			return errors.New("empty write")
		} else {
			written += n
		}
	}
	return nil
}

// Attempts to read a value from environment variable named envName
// If there's nothing there, it attempt to append _FILE to the variable
// name and see if it contains a filename; if so, it reads the
// contents of the file into cnd.
func LoadEnvVar(cnd *string, envName, defVal string) error {
	if cnd == nil {
		return errors.New("Invalid argument")
	} else if len(*cnd) > 0 {
		return nil
	} else if len(envName) == 0 {
		return nil
	}
	*cnd = os.Getenv(envName)
	if *cnd != `` {
		// we read something out of the variable, return
		return nil
	}

	// Set default value
	*cnd = defVal

	// No joy in the environment variable, append _FILE and try
	filename := os.Getenv(fmt.Sprintf("%s_FILE", envName))
	if filename == `` {
		// Nothing, screw it, return the default value
		return nil
	}
	file, err := os.Open(filename)
	if err != nil {
		// they specified a file but we can't open it
		return err
	}
	defer file.Close()

	s := bufio.NewScanner(file)
	s.Scan()
	l := s.Text()
	if l == `` {
		// there was nothing in the file?
		return errors.New("Empty file or blank first line of file")
	}
	*cnd = l

	return nil
}

func LoadEnvVarList(lst *[]string, envName string) error {
	if lst == nil {
		return errors.New("Invalid argument")
	} else if len(*lst) > 0 {
		return nil
	} else if len(envName) == 0 {
		return nil
	}
	arg := os.Getenv(envName)
	if len(arg) == 0 {
		// Nothing in the env variable, let's try reading from a file
		filename := os.Getenv(fmt.Sprintf("%s_FILE", envName))
		if filename == `` {
			// Nothing, return
			return nil
		}
		file, err := os.Open(filename)
		if err != nil {
			// they specified a file but we can't open it
			return err
		}
		defer file.Close()

		s := bufio.NewScanner(file)
		s.Scan()
		l := s.Text()
		if l == `` {
			// there was nothing in the file?
			return errors.New("Empty file or blank first line of file")
		}
		arg = l
	}
	if bits := strings.Split(arg, ","); len(bits) > 0 {
		for _, b := range bits {
			if b = strings.TrimSpace(b); len(b) > 0 {
				*lst = append(*lst, b)
			}
		}
	}
	return nil
}

func AppendDefaultPort(bstr string, defPort uint16) string {
	if _, _, err := net.SplitHostPort(bstr); err != nil {
		if strings.HasSuffix(err.Error(), `missing port in address`) {
			return fmt.Sprintf("%s:%d", bstr, defPort)
		}
	}
	return bstr
}

type multSuff struct {
	mult   int64
	suffix string
}

var (
	rateSuffix = []multSuff{
		multSuff{mult: 1024, suffix: `k`},
		multSuff{mult: 1024, suffix: `kb`},
		multSuff{mult: 1024, suffix: `kbit`},
		multSuff{mult: 1024, suffix: `kbps`},
		multSuff{mult: 1024 * 1024, suffix: `m`},
		multSuff{mult: 1024 * 1024, suffix: `mb`},
		multSuff{mult: 1024 * 1024, suffix: `mbit`},
		multSuff{mult: 1024 * 1024, suffix: `mbps`},
		multSuff{mult: 1024 * 1024 * 1024, suffix: `g`},
		multSuff{mult: 1024 * 1024 * 1024, suffix: `gb`},
		multSuff{mult: 1024 * 1024 * 1024, suffix: `gbit`},
		multSuff{mult: 1024 * 1024 * 1024, suffix: `gbps`},
	}
)

//we return the rate in bytes per second
func parseRate(s string) (Bps int64, err error) {
	var r uint64
	if len(s) == 0 {
		return
	}
	s = strings.ToLower(s)
	for _, v := range rateSuffix {
		if strings.HasSuffix(s, v.suffix) {
			s = strings.TrimSuffix(s, v.suffix)
			if r, err = strconv.ParseUint(s, 10, 64); err != nil {
				return
			}
			Bps = (int64(r) * v.mult) / 8
			return
		}
	}
	if r, err = strconv.ParseUint(s, 10, 64); err != nil {
		return
	}
	Bps = int64(r / 8)
	if Bps < minThrottle {
		err = errors.New("Ingest cannot be limited below 1mbit")
	}
	return
}

// ParseSource returns a net.IP byte buffer
// the returned buffer will always be a 32bit or 128bit buffer
// but we accept encodings as IPv4, IPv6, integer, hex encoded hash
// this function simply walks the available encodings until one works
func ParseSource(v string) (b net.IP, err error) {
	var i uint64
	// try as an IP
	if b = net.ParseIP(v); b != nil {
		return
	}
	//try as a plain integer
	if i, err = parseUint64(v); err == nil {
		//encode into a buffer
		bb := make([]byte, 16)
		binary.BigEndian.PutUint64(bb[8:], i)
		b = net.IP(bb)
		return
	}
	//try as a hex encoded byte array
	if (len(v)&1) == 0 && len(v) <= 32 {
		var vv []byte
		if vv, err = hex.DecodeString(v); err == nil {
			bb := make([]byte, 16)
			offset := len(bb) - len(vv)
			copy(bb[offset:], vv)
			b = net.IP(bb)
			return
		}
	}
	err = fmt.Errorf("Failed to decode %s as a source value", v)
	return
}

func parseUint64(v string) (i uint64, err error) {
	if strings.HasPrefix(v, "0x") {
		i, err = strconv.ParseUint(strings.TrimPrefix(v, "0x"), 16, 64)
	} else {
		i, err = strconv.ParseUint(v, 10, 64)
	}
	return
}

// lineParameter checks if the line contains the parameter provided
// the parameter is considered provided if after a ToLower and TrimSpace the parameter is the prefix
// empty lines and/or empty parameters are not checked
// the match is case insensitive
func lineParameter(line, parameter string) bool {
	l := strings.ToLower(strings.TrimSpace(line))
	p := strings.ToLower(strings.TrimSpace(parameter))
	if len(l) == 0 || len(p) == 0 {
		return false
	}
	return strings.HasPrefix(l, p)
}

// globalLineBoundary returns the line numbers representing the start and stop boundaries of the global section
// if the global section cannot be found, both returned values are -1
// start is inclusive, stop is exclusive, so normal ranging is appropriate with the bound values
func globalLineBoundary(lines []string) (start, stop int, ok bool) {
	start = -1
	stop = -1
	//find the start of the global section
	for i := range lines {
		if lineParameter(lines[i], globalHeader) {
			start = i
			break
		}
	}
	if start == -1 {
		//did not find the start
		return
	}

	//try to find the end
	for i := start + 1; i < len(lines); i++ {
		if lineParameter(lines[i], headerStart) {
			stop = i
			ok = true
			return
		}
	}
	//not stop found, set to the end
	stop = len(lines)
	if start < 0 || stop < 0 || start > len(lines) || stop > len(lines) || start >= stop {
		//nothing here is valid
		return
	}
	ok = true
	return
}

// argInGlobalLines identifies which line in the global config contains the given parameter argument
// if the argument is not found, -1 is returned
func argInGlobalLines(lines []string, arg string) (lineno int) {
	lineno = -1
	gstart, gend, ok := globalLineBoundary(lines)
	if !ok {
		return
	}
	for i := gstart; i < gend; i++ {
		if lineParameter(lines[i], arg) {
			lineno = i
			return
		}
	}
	return
}

func insertLine(lines []string, line string, loc int) (nl []string, err error) {
	if loc < 0 || loc >= len(lines) {
		err = ErrInvalidLineLocation
		return
	}
	nl = append(nl, lines[0:loc]...)
	nl = append(nl, line)
	nl = append(nl, lines[loc:]...)

	return
}

func getLeadingString(l, param string) (s string) {
	if idx := strings.Index(strings.ToLower(l), strings.ToLower(param)); idx != -1 {
		s = l[0:idx]
	}
	return
}

func getCommentString(l, param string) (s string) {
	if idx := strings.Index(strings.ToLower(l), strings.ToLower(param)); idx != -1 {
		s = l[idx:]
	}
	return
}

// updateLine updates the parameter value at a given line
// the given line MUST contain the paramter value, or we error out
func updateLine(lines []string, param, value string, loc int) (nl []string, err error) {
	//check that the line location is valid
	if loc >= len(lines) || loc < 0 {
		err = ErrInvalidLineLocation
	}
	//check if the specified line has that parameter
	if !lineParameter(lines[loc], param) {
		err = ErrInvalidUpdateLineParameter
		return
	}
	//get the leading stuff
	leadingString := getLeadingString(lines[loc], param)
	//get any trailing comments
	commentString := getCommentString(lines[loc], commentValue)
	nl = lines
	nl[loc] = fmt.Sprintf(`%s%s=%s %s`, leadingString, param, value, commentString)
	return
}
