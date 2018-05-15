/*************************************************************************
 * Copyright 2017 Gravwell, Inc. All rights reserved.
 * Contact: <legal@gravwell.io>
 *
 * This software may be modified and distributed under the terms of the
 * BSD 2-clause license. See the LICENSE file for details.
 **************************************************************************/

package main

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"os"
	"strconv"
	"sync"

	"github.com/gravwell/ingest/entry"
	"github.com/gravwell/timegrinder"
)

const (
	stateEmpty  int = iota
	stateInPrio int = iota
	stateInMsg  int = iota
)

func rfc5424ConnHandlerTCP(c net.Conn, ch chan *entry.Entry, ignoreTS, setLocalTime bool, dropPrio bool, tag entry.EntryTag, wg *sync.WaitGroup, src net.IP) {
	wg.Add(1)
	id := addConn(c)
	defer wg.Done()
	defer delConn(id)
	defer c.Close()
	ipstr, _, err := net.SplitHostPort(c.RemoteAddr().String())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get host from rmote addr \"%s\": %v\n", c.RemoteAddr().String(), err)
		return
	}
	rip := net.ParseIP(ipstr)
	if src != nil {
		rip = src
	}
	if rip == nil {
		fmt.Fprintf(os.Stderr, "Failed to get remote addr from \"%s\"\n", ipstr)
		return
	}

	tcfg := timegrinder.Config{
		EnableLeftMostSeed: true,
	}
	tg, err := timegrinder.NewTimeGrinder(tcfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get a handle on the timegrinder: %v\n", err)
		return
	}

	if setLocalTime {
		tg.SetLocalTime()
	}

	s := bufio.NewScanner(c)
	state := stateEmpty
	var start int
	splitter := func(data []byte, atEOF bool) (advance int, token []byte, err error) {
		defer func() { start = start - advance }()
		debugout("data = %v", string(data))
		for i := range data {
			switch state {
			case stateEmpty: //empty
				if data[i] == '<' {
					advance = i
					state = stateInPrio
					start = i
				}
			case stateInPrio: //prioStart
				if data[i] == '>' {
					state = stateInMsg
					if dropPrio {
						start = i + 1
						advance = i + 1
					}
				}
			case stateInMsg: //inmsg
				if data[i] == '<' {
					debugout("setting token from index %v to %v\n", start, i)
					token = data[start:i]
					state = stateEmpty
					advance = i
					return
				}
			}
		}
		if state == stateInMsg && atEOF { //inmsg
			token = data
			err = bufio.ErrFinalToken
			return
		}
		return
	}
	s.Split(splitter)
	for s.Scan() {
		data := bytes.Trim(s.Bytes(), "\n\r\t ")
		debugout("Scanning TCP input %s\n", string(data))
		if len(data) == 0 {
			continue
		}
		if err := handleLog(data, rip, ignoreTS, tag, ch, tg); err != nil {
			return
		}
	}
}

func rfc5424ConnHandlerUDP(c *net.UDPConn, ch chan *entry.Entry, ignoreTS, setLocalTime, dropPrio bool, tag entry.EntryTag, wg *sync.WaitGroup, src net.IP) {
	buff := make([]byte, 16*1024) //local buffer that should be big enough for even the largest UDP packets
	tcfg := timegrinder.Config{
		EnableLeftMostSeed: true,
	}
	tg, err := timegrinder.NewTimeGrinder(tcfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get a handle on the timegrinder: %v\n", err)
		return
	}

	if setLocalTime {
		tg.SetLocalTime()
	}

	for {
		n, raddr, err := c.ReadFromUDP(buff)
		if err != nil {
			break
		}
		if n > 0 {
			if raddr == nil {
				continue
			}
			if n > len(buff) {
				continue
			}
			rip := raddr.IP
			if src != nil {
				rip = src
			}
			handleRFC5424Packet(append([]byte(nil), buff[:n]...), rip, ch, ignoreTS, dropPrio, tag, tg)
		}
	}

}

//we can be very very fast on this one by just manually scanning the buffer
func handleRFC5424Packet(buff []byte, ip net.IP, ch chan *entry.Entry, ignoreTS, dropPrio bool, tag entry.EntryTag, tg *timegrinder.TimeGrinder) {
	var msgStart int
	var state int

	debugout("Scanning UDP packet %s\n", string(buff))
	state = stateEmpty
	for i := range buff {
		switch state {
		case stateEmpty:
			if buff[i] == '<' {
				msgStart = i
				state = stateInPrio
			}
		case stateInPrio:
			if buff[i] == '>' {
				if _, err := strconv.Atoi(string(buff[msgStart+1 : i])); err != nil {
					//we are toasted, start over
					state = stateEmpty
					continue
				}
				state = stateInMsg
				if dropPrio {
					msgStart = i + 1
				}
			}
		case stateInMsg:
			if buff[i] == '<' {
				//throw current message
				handleLog(bytes.TrimSpace(buff[msgStart:i]), ip, ignoreTS, tag, ch, tg)
				msgStart = i
				state = stateInPrio
			}
		}
	}
	if state == stateInMsg {
		handleLog(bytes.TrimSpace(buff[msgStart:]), ip, ignoreTS, tag, ch, tg)
	}
}
