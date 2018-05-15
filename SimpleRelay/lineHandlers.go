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
	"io"
	"net"
	"os"
	"sync"

	"github.com/gravwell/ingest/entry"
	"github.com/gravwell/timegrinder"
)

func lineConnHandlerTCP(c net.Conn, ch chan *entry.Entry, ignoreTimestamps, setLocalTime bool, tag entry.EntryTag, wg *sync.WaitGroup, src net.IP) {
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

	var tg *timegrinder.TimeGrinder
	if !ignoreTimestamps {
		tcfg := timegrinder.Config{
			EnableLeftMostSeed: true,
		}
		tg, err = timegrinder.NewTimeGrinder(tcfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to get a handle on the timegrinder: %v\n", err)
			return
		}
		if setLocalTime {
			tg.SetLocalTime()
		}

	}
	bio := bufio.NewReader(c)
	for {
		data, err := bio.ReadBytes('\n')
		data = bytes.Trim(data, "\n\r\t ")

		if len(data) > 0 {
			if err := handleLog(data, rip, ignoreTimestamps, tag, ch, tg); err != nil {
				return
			}
		}
		if err != nil {
			if err != io.EOF {
				lerr, ok := err.(*net.OpError)
				if !ok || lerr.Temporary() {
					fmt.Fprintf(os.Stderr, "Failed to read line: %v\n", err)
				}
			}
			return
		}

	}
}

func lineConnHandlerUDP(c *net.UDPConn, ch chan *entry.Entry, ignoreTimestamps, setLocalTime bool, tag entry.EntryTag, wg *sync.WaitGroup, src net.IP) {
	sp := []byte("\n")
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
		if n == 0 {
			continue
		}
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

		lns := bytes.Split(buff[:n], sp)
		for _, ln := range lns {
			ln = bytes.Trim(ln, "\n\r\t ")
			if len(ln) == 0 {
				continue
			}
			//because we are using and reusing a local buffer, we have to copy the bytes when handing in
			if err := handleLog(append([]byte(nil), ln...), rip, ignoreTimestamps, tag, ch, tg); err != nil {
				return
			}
		}
	}

}
