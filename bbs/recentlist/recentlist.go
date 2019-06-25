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

package recentlist

import (
	"errors"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"encoding/json"

	"github.com/boltdb/bolt"
	"bbs/cfg"
	"bbs/db"
	"bbs/node"
	"bbs/node/manager"
	"bbs/record"
	"bbs/tag/suggest"
)

const defaultUpdateRange = 24 * time.Hour // Seconds

//IsInUpdateRange returns true if stamp is in updateRange.
func IsInUpdateRange(nstamp int64) bool {
	now := time.Now()
	if now.Add(-defaultUpdateRange).Unix() < nstamp && nstamp < now.Add(defaultUpdateRange).Unix() {
		return true
	}
	return false
}

//RecentList represents records list udpated by remote host and
//gotten by /gateway.cgi/Recent

//Datfiles returns all datfile names in recentlist.
func Datfiles() []string {
	var datfile []string
	err := db.DB.View(func(tx *bolt.Tx) error {
		var err error
		datfile, err = db.GetPrefixs(tx, "recent")
		return err
	})
	if err != nil {
		log.Print(err)
	}
	return datfile
}

//Newest returns newest record of datfile in the list.
//if not found returns nil.
func Newest(datfile string) (*record.Head, error) {
	var rows []string
	err := db.DB.View(func(tx *bolt.Tx) error {
		var err error
		rows, err = db.GetStrings(tx, "recent", []byte(datfile))
		return err
	})
	if err != nil {
		log.Print(err)
		return nil, err
	}
	if len(rows) == 0 {
		return nil, errors.New("not found")
	}
	newest := []byte(rows[len(rows)-1])
	r := record.Head{}
	err = json.Unmarshal(newest, &r)
	if err != nil {
		log.Println(err)
	}
	return &r, err
}

//appendHead add a infos generated from the record.
func appendHead(tx *bolt.Tx, rec *record.Head) {
	if find(rec) {
		return
	}
	k := rec.ToKey()
	err := db.Put(tx, "recent", k, rec)
	if err != nil {
		log.Print(err)
	}
}

//Append add a infos generated from the record.
func Append(rec *record.Head) {
	err := db.DB.Update(func(tx *bolt.Tx) error {
		appendHead(tx, rec)
		return nil
	})
	if err != nil {
		log.Println(err)
	}
}

//find finds records and returns index. returns -1 if not found.
func find(rec *record.Head) bool {
	k := rec.ToKey()
	var r int
	err := db.DB.View(func(tx *bolt.Tx) error {
		var err error
		r, err = db.Count(tx, "recent", k)
		return err
	})
	if err != nil {
		log.Print(err)
	}

	return r > 0
}

//RemoveOlds remove old records..
func RemoveOlds() {
	if cfg.RecentRange <= 0 {
		return
	}
	t := time.Now().Unix() - cfg.RecentRange
	err := db.DB.Update(func(tx *bolt.Tx) error {
		ba := tx.Bucket([]byte("recent"))
		if ba == nil {
			return errors.New("bucket is not found")
		}
		c := ba.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			var m record.Head
			if err := json.Unmarshal(v, &m); err != nil {
				return err
			}
			if m.Stamp > t {
				continue
			}
			if err := c.Delete(); err != nil {
				log.Println(err)
			}
		}
		return nil
	})
	if err != nil {
		log.Println(err)
	}
}

//Getall retrieves Recent records from nodes in searchlist and stores them.
//tags are shuffled and truncated to tagsize and stored to sugtags in cache.
//also source nodes are stored into lookuptable.
//also tags which Recentlist doen't have in sugtagtable are truncated
func Getall(all bool) {
	const searchNodes = 100

	var begin int64
	if cfg.RecentRange > 0 && !all {
		begin = time.Now().Unix() - cfg.RecentRange
	}
	nodes := manager.Random(nil, searchNodes)
	var wg sync.WaitGroup
	for _, n := range nodes {
		wg.Add(1)
		go get(begin, &wg, n)
	}
	wg.Wait()
	suggest.Prune(GetRecords())
}

func get(begin int64, wg *sync.WaitGroup, n *node.Node) {
	defer wg.Done()
	var res []string
	var err error
	res, err = n.Talk("/recent/"+strconv.FormatInt(begin, 10)+"-", nil)
	if err != nil {
		manager.RemoveFromAllTable(n)
		log.Println(err)
		return
	}
	err = db.DB.Update(func(tx *bolt.Tx) error {
		for _, line := range res {
			rec, errr := record.Make(line)
			if errr != nil {
				continue
			}
			appendHead(tx, rec.Head)
			tags := strings.Fields(strings.TrimSpace(rec.GetBodyValue("tag", "")))
			if len(tags) > 0 {
				suggest.AddString(tx, rec.Datfile, tags)
				manager.AppendToTableTX(tx, rec.Datfile, n)
			}
		}
		return nil
	})
	if err != nil {
		log.Println(err)
	}
	log.Println("added", len(res), "recent records from", n.Nodestr)
}

//GetRecords copies and returns recorcds in recentlist.
func GetRecords() []*record.Head {
	var inf []*record.Head

	err := db.DB.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("recent"))
		if b == nil {
			return errors.New("bucket is not found")
		}
		errr := b.ForEach(func(k, v []byte) error {
			r := record.Head{}
			if err := json.Unmarshal(v, &r); err != nil {
				return err
			}
			inf = append(inf, &r)
			return nil
		})
		return errr
	})
	if err != nil {
		log.Print(err)
		return nil
	}
	return inf
}
