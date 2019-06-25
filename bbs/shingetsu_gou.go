// +build android

/*
 * Copyright (c) 2015, Shinya Yagyu
 * All rights reserved.
 * Redistribution and use in source and binary forms, with or without
 * modification, are permitted provided that the following conditions are met:
 *
 * 1. Redistributions of source code must retain the above copyright notice,
 *    this list of conditions and the following disclaimer.
 * 2. Redistributions in binary form must reproduce the above copyright notice,
 *    this list of conditions and the following disclaimer in the documentation
 *    and/or other materials provided with the distribution.
 * 3. Neither the name of the copyright holder nor the names of its
 *    contributors may be used to endorse or promote products derived from this
 *    software without specific prior written permission.
 *
 * THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS"
 * AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
 * IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE
 * ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE
 * LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR
 * CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF
 * SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS
 * INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN
 * CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE)
 * ARISING IN ANY WAY OUT OF THE USE OF THIS SOFTWARE, EVEN IF ADVISED OF THE
 * POSSIBILITY OF SUCH DAMAGE.
 */

package bbs

import (
	"log"
	"time"

	"net"

	"bbs/cfg"
	"bbs/db"
	"bbs/gou"
)

var listener net.Listener
var ch chan error

//ExpandFiles expands files in files dir.
func ExpandFiles(rpath string,location string,timeoffset int) {
	time.Local = time.FixedZone(location, timeoffset)
	cfg.SetAndroid(rpath)
	cfg.Parse()
	gou.SetupDirectories()
	gou.SetLogger(true, false)
	log.Println("********************starting Gou", cfg.Version, "...******************")
	gou.ExpandAssets()
}

//Port returns port number.
func Port() int {
	return cfg.DefaultPort
}

//Run setups params and start daemon for android.
//You must call ExpandFiles beforehand.
func Run() {
	db.Setup()
	listener, ch = gou.StartDaemon()
}

//Stop stops the http server.
func Stop() {
	if listener != nil {
		listener.Close()
		db.DB.Close()
		log.Println(<-ch)
	}
}
