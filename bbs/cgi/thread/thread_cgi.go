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

package thread

import (
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"html/template"
	"log"
	"math"
	"math/rand"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	"bbs/cfg"
	"bbs/cgi"
	"bbs/record"
	"bbs/tag/user"
	"bbs/thread"
	"bbs/thread/download"
	"bbs/updateque"
	"bbs/util"
)

//Setup setups handlers for thread.cgi
func Setup(s *cgi.LoggingServeMux) {
	rtr := mux.NewRouter()

	cgi.RegistToRouter(rtr, cfg.ThreadURL+"/", printThreadIndex)

	reg := cfg.ThreadURL + "/{datfile:thread_[0-9A-F]+}/{id:[0-9a-f]{32}}/s{stamp:\\d+}.{thumbnailSize:\\d+x\\d+}.{suffix:.*}"
	cgi.RegistToRouter(rtr, reg, printAttach)

	reg = cfg.ThreadURL + "/{datfile:thread_[0-9A-F]+}/{id:[0-9a-f]{32}}/{stamp:\\d+}.{suffix:.*}"
	cgi.RegistToRouter(rtr, reg, printAttach)

	reg = cfg.ThreadURL + "/{path:[^/]+}{end:/?$}"
	cgi.RegistToRouter(rtr, reg, printThread)

	reg = cfg.ThreadURL + "/{path:[^/]+}/{id:[0-9a-f]{8}}{end:$}"
	cgi.RegistToRouter(rtr, reg, printThread)

	reg = cfg.ThreadURL + "/{path:[^/]+}/p{page:[0-9]+}{end:$}"
	cgi.RegistToRouter(rtr, reg, printThread)

	s.Handle(cfg.ThreadURL+"/", handlers.CompressHandler(rtr))
}

//printThreadIndex adds records in multiform and redirect to its thread page.
func printThreadIndex(w http.ResponseWriter, r *http.Request) {
	if a, err := new(w, r); err == nil {
		a.printThreadIndex()
	}
}

func printAttach(w http.ResponseWriter, r *http.Request) {
	a, err := new(w, r)
	if err != nil {
		return
	}
	m := mux.Vars(r)
	var stamp int64
	if m["stamp"] != "" {
		var err error
		stamp, err = strconv.ParseInt(m["stamp"], 10, 64)
		if err != nil {
			log.Println(err)
			return
		}
	}
	a.printAttach(m["datfile"], m["id"], stamp, m["thumbnailSize"], m["suffix"])
}

//printThread renders whole thread list page.
func printThread(w http.ResponseWriter, r *http.Request) {
	a, errr := new(w, r)
	if errr != nil {
		return
	}
	m := mux.Vars(r)
	var page int
	if m["page"] != "" {
		var err error
		page, err = strconv.Atoi(m["page"])
		if err != nil {
			return
		}
	}
	path, err := url.QueryUnescape(m["path"])
	if err != nil {
		log.Print(err)
		return
	}
	a.printThread(path, m["id"], page)
}

//threadCGI is for thread.cgi.
type threadCGI struct {
	*cgi.CGI
}

//new returns threadCGI obj.
func new(w http.ResponseWriter, r *http.Request) (*threadCGI, error) {
	c, err := cgi.NewCGI(w, r)
	if err != nil {
		c.Print403()
		return nil, err
	}
	if !c.CheckVisitor() {
		c.Print403()
		return nil, errors.New("visitor now allowed")
	}
	t := threadCGI{
		CGI: c,
	}

	t.IsThread = true
	return &t, nil
}

//printThreadIndex adds records in multiform and redirect to its thread page.
func (t *threadCGI) printThreadIndex() {
	err := t.Req.ParseMultipartForm(int64(cfg.RecordLimit) << 10)
	if err != nil {
		t.Print404(nil, "")
		return
	}
	if t.Req.FormValue("cmd") != "post" || !strings.HasPrefix(t.Req.FormValue("file"), "thread_") {
		t.Print404(nil, "")
		return
	}
	id := t.doPost()
	if id == "" {
		t.Print404(nil, "")
		return
	}
	datfile := t.Req.FormValue("file")
	title := util.StrEncode(util.FileDecode(datfile))
	t.Print302(cfg.ThreadURL + "/" + title + "#r" + id)
}

//setCookie set cookie access=now time,tmpaccess=access var.
func (t *threadCGI) setCookie(ca *thread.Cache, access string) []*http.Cookie {
	const (
		saveCookie = 7 * 24 * time.Hour // Seconds
	)

	c := http.Cookie{}
	c.Expires = time.Now().Add(saveCookie)
	c.Path = cfg.ThreadURL + "/" + util.StrEncode(util.FileDecode(ca.Datfile))
	c.Name = "access"
	c.Value = strconv.FormatInt(time.Now().Unix(), 10)
	if access == "" {
		return []*http.Cookie{&c}
	}
	cc := http.Cookie{}
	cc.Name = "tmpaccess"
	cc.Value = access
	return []*http.Cookie{&c, &cc}
}

//printPageNavi renders page_navi.txt, part for paging.
func (t *threadCGI) printPageNavi(path string, page int, ca *thread.Cache, id string) {
	len := ca.Len(record.Alive)
	first := len / cfg.ThreadPageSize
	if len%cfg.ThreadPageSize == 0 {
		first++
	}
	pages := make([]int, first+1)
	for i := 0; i <= first; i++ {
		pages[i] = i
	}
	s := struct {
		Page           int
		CacheLen       int
		Path           string
		ID             string
		First          int
		ThreadCGI      string
		Message        cgi.Message
		ThreadPageSize int
		Pages          []int
	}{
		page,
		len,
		path,
		id,
		first,
		cfg.ThreadURL,
		t.M,
		cfg.ThreadPageSize,
		pages,
	}
	cgi.RenderTemplate("page_navi", s, t.WR)
}

//printTag renders thread_tags.txt , part for displayng tags.
func (t *threadCGI) printTag(ca *thread.Cache) {
	s := struct {
		Datfile   string
		Tags      []string
		Classname string
		Target    string
		cgi.Defaults
	}{
		ca.Datfile,
		user.GetStrings(ca.Datfile),
		"tags",
		"changes",
		*t.Defaults(),
	}
	cgi.RenderTemplate("thread_tags", s, t.WR)
}

//printThreadHead renders head part of thread page with cookie.
func (t *threadCGI) printThreadHead(path, id string, page int, ca *thread.Cache, rss string) error {
	switch {
	case ca.HasRecord():
		if !t.IsBot() {
			download.GetCache(true, ca)
		} else {
			log.Println("bot detected, not get cache")
		}
	case t.CheckGetCache():
		ca.Subscribe()
		if t.Req.FormValue("search_new_file") == "" {
			download.GetCache(true, ca)
		}
	default:
		t.Print404(nil, id)
		return errors.New("no records")
	}
	var access string
	var newcookie []*http.Cookie
	if ca.HasRecord() && id == "" && page == 0 {
		cookie, err := t.Req.Cookie("access")
		if err == nil {
			access = cookie.Value
		} else {
			log.Println(err)
		}
		newcookie = t.setCookie(ca, access)
	}
	t.Header(path, rss, newcookie, false)
	return nil
}

//printThreadTop renders toppart of thread page.
func (t *threadCGI) printThreadTop(path, id string, nPage int, ca *thread.Cache) {
	var lastrec *record.Record
	var resAnchor string
	recs := ca.LoadRecords(record.Alive)
	ids := recs.Keys()
	if ca.HasRecord() && nPage == 0 && id == "" && len(ids) > 0 {
		lastrec = recs[ids[len(ids)-1]]
		resAnchor = t.ResAnchor(lastrec.ID[:8], cfg.ThreadURL, t.Path(), false)
	}
	s := struct {
		Path      string
		Cache     *thread.Cache
		Lastrec   *record.Record
		ResAnchor template.HTML
		cgi.Defaults
	}{
		path,
		ca,
		lastrec,
		template.HTML(resAnchor),
		*t.Defaults(),
	}
	cgi.RenderTemplate("thread_top", s, t.WR)
}

//printThreadBody renders body(records list) part of thread page with paging.
func (t *threadCGI) printThreadBody(id string, nPage int, ca *thread.Cache) {
	recs := ca.LoadRecords(record.Alive)
	ids := recs.Keys()
	fmt.Fprintln(t.WR, "</p>\n<dl id=\"records\">")
	from := len(ids) - cfg.ThreadPageSize*(nPage+1)
	to := len(ids) - cfg.ThreadPageSize*(nPage)
	if from < 0 {
		from = 0
	}
	if to < 0 {
		to = 0
	}
	var inrange []string
	switch {
	case id != "":
		inrange = ids
	case nPage > 0:
		inrange = ids[from:to]
	default:
		inrange = ids[from:]
	}

	for _, k := range inrange {
		rec := recs.Get(k, nil)
		if (id == "" || rec.ID[:8] == id) && rec.Load() == nil {
			t.printRecord(ca, rec)
		}
	}

	fmt.Fprintln(t.WR, "</dl>")
}

//printThread renders whole thread list page.
func (t *threadCGI) printThread(path, id string, nPage int) {
	if id != "" && t.Req.FormValue("ajax") != "" {
		t.printThreadAjax(id)
		return
	}
	filePath := util.FileEncode("thread", path)
	ca := thread.NewCache(filePath)
	rss := cfg.GatewayURL + "/rss"
	if t.printThreadHead(path, id, nPage, ca, rss) != nil {
		return
	}
	tags := strings.Fields(strings.TrimSpace(t.Req.FormValue("tag")))
	if t.IsAdmin() && len(tags) > 0 {
		user.Add(ca.Datfile, tags)
	}
	t.printTag(ca)
	t.printThreadTop(path, id, nPage, ca)
	t.printPageNavi(path, nPage, ca, id)
	t.printThreadBody(id, nPage, ca)

	escapedPath := html.EscapeString(path)
	escapedPath = strings.Replace(escapedPath, "  ", "&nbsp;&nbsp;", -1)
	ss := struct {
		Cache   *thread.Cache
		Message cgi.Message
	}{
		ca,
		t.M,
	}
	cgi.RenderTemplate("thread_bottom", ss, t.WR)

	if ca.HasRecord() {
		t.printPageNavi(path, nPage, ca, id)
		fmt.Fprintf(t.WR, "</p>")
	}
	t.printPostForm(ca)
	t.printTag(ca)
	t.RemoveFileForm(ca, escapedPath)
	t.Footer(t.MakeMenubar("bottom", rss))
}

//printThreadAjax renders records in cache id for ajax.
func (t *threadCGI) printThreadAjax(id string) {
	th := strings.Split(t.Path(), "/")[0]
	filePath := util.FileEncode("thread", th)
	ca := thread.NewCache(filePath)
	if !ca.HasRecord() {
		log.Println(filePath, "not found")
		return
	}
	fmt.Fprintln(t.WR, "<dl>")
	recs := ca.LoadRecords(record.Alive)
	for _, rec := range recs {
		if id == "" || rec.ID[:8] == id && rec.Load() == nil {
			t.printRecord(ca, rec)
		}
	}
	fmt.Fprintln(t.WR, "</dl>")
}

//printRecord renders record.txt , with records in cache ca.
func (t *threadCGI) printRecord(ca *thread.Cache, rec *record.Record) {
	thumbnailSize := ""
	var suffix string
	var attachSize int64
	if at := rec.GetBodyValue("attach", ""); at != "" {
		suffix = rec.GetBodyValue("suffix", "")
		attachFile := rec.AttachPath("")
		attachSize = int64(len(at)*57/78) + 1000
		reg := regexp.MustCompile("^[0-9A-Za-z]+")
		if !reg.MatchString(suffix) {
			suffix = cfg.SuffixTXT
		}
		typ := mime.TypeByExtension("." + suffix)
		if typ == "" {
			typ = "text/plain"
		}
		if util.IsValidImage(typ, attachFile) {
			thumbnailSize = cfg.DefaultThumbnailSize
		}
	}
	body := rec.GetBodyValue("body", "")
	body = t.HTMLFormat(body, cfg.ThreadURL, t.Path(), false)
	removeID := rec.GetBodyValue("remove_id", "")
	if len(removeID) > 8 {
		removeID = removeID[:8]
	}
	resAnchor := t.ResAnchor(removeID, cfg.ThreadURL, t.Path(), false)

	id8 := rec.ID
	if len(id8) > 8 {
		id8 = id8[:8]
	}
	s := struct {
		Datfile    string
		Rec        *record.Record
		RecHead    record.Head
		Sid        string
		AttachSize int64
		Suffix     string
		Body       template.HTML
		Thumbnail  string
		RemoveID   string
		ResAnchor  string
		cgi.Defaults
	}{
		ca.Datfile,
		rec,
		rec.CopyHead(),
		id8,
		attachSize,
		suffix,
		template.HTML(body),
		thumbnailSize,
		removeID,
		resAnchor,
		*t.Defaults(),
	}
	cgi.RenderTemplate("record", s, t.WR)
}

//printPostForm renders post_form.txt,page for posting attached file.
func (t *threadCGI) printPostForm(ca *thread.Cache) {
	mimes := []string{
		".css", ".gif", ".htm", ".html", ".jpg", ".js", ".pdf", ".png", ".svg",
		".txt", ".xml",
	}
	s := struct {
		Cache    *thread.Cache
		Suffixes []string
		Limit    int
		cgi.Defaults
	}{
		ca,
		mimes,
		cfg.RecordLimit * 3 >> 2,
		*t.Defaults(),
	}
	cgi.RenderTemplate("post_form", s, t.WR)
}

//renderAttach render the content of attach file with content-type=typ.
func (t *threadCGI) renderAttach(rec *record.Record, suffix string, stamp int64, thumbnailSize string) {
	attachFile := rec.AttachPath(thumbnailSize)
	if attachFile == "" {
		return
	}
	typ := mime.TypeByExtension("." + suffix)
	if typ == "" {
		typ = "text/plain"
	}
	t.WR.Header().Set("Content-Type", typ)
	t.WR.Header().Set("Last-Modified", t.RFC822Time(stamp))
	if !util.IsValidImage(typ, attachFile) {
		t.WR.Header().Set("Content-Disposition", "attachment")
	}
	decoded, err := base64.StdEncoding.DecodeString(rec.GetBodyValue("attach", ""))
	if err != nil {
		log.Println(err)
		t.Print404(nil, "")
		return
	}
	if thumbnailSize != "" && (cfg.ForceThumbnail || thumbnailSize == cfg.DefaultThumbnailSize) {
		decoded = util.MakeThumbnail(decoded, suffix, thumbnailSize)
	}
	_, err = t.WR.Write(decoded)
	if err != nil {
		log.Println(err)
		t.Print404(nil, "")
	}
}

//printAttach renders the content of attach file and makes thumnail if needed and possible.
func (t *threadCGI) printAttach(datfile, id string, stamp int64, thumbnailSize, suffix string) {
	ca := thread.NewCache(datfile)
	switch {
	case ca.HasRecord():
	case t.CheckGetCache():
		download.GetCache(true, ca)
	default:
		t.Print404(ca, "")
		return
	}
	rec := record.New(ca.Datfile, id, stamp)
	if !rec.Exists() {
		t.Print404(ca, "")
		return
	}
	if err := rec.Load(); err != nil {
		t.Print404(ca, "")
		return
	}
	if rec.GetBodyValue("suffix", "") != suffix {
		t.Print404(ca, "")
		return
	}
	t.renderAttach(rec, suffix, stamp, thumbnailSize)
}

//errorTime calculates gaussian distribution by box-muller transformation.
func (t *threadCGI) errorTime() int64 {
	const timeErrorSigma = 60 // Seconds

	x1 := rand.Float64()
	x2 := rand.Float64()
	return int64(timeErrorSigma*math.Sqrt(-2*math.Log(x1))*math.Cos(2*math.Pi*x2)) + time.Now().Unix()
}

//guessSuffix guess suffix of attached at from formvalue "suffix"
func (t *threadCGI) guessSuffix(at *attached) string {
	guessSuffix := cfg.SuffixTXT
	if at != nil {
		if e := path.Ext(at.Filename); e != "" {
			guessSuffix = strings.ToLower(e)
		}
	}

	suffix := t.Req.FormValue("suffix")
	switch {
	case suffix == "" || suffix == "AUTO":
		suffix = guessSuffix
	case strings.HasPrefix(suffix, "."):
		suffix = suffix[1:]
	}
	suffix = strings.ToLower(suffix)
	reg := regexp.MustCompile("[^0-9A-Za-z]")
	return reg.ReplaceAllString(suffix, "")
}

//makeRecord builds and returns record with attached file.
//if nobody render null_article page.
func (t *threadCGI) makeRecord(at *attached, suffix string, ca *thread.Cache) (*record.Record, error) {
	body := make(map[string]string)
	for _, name := range []string{"body", "base_stamp", "base_id", "name", "mail"} {
		if value := t.Req.FormValue(name); value != "" {
			body[name] = util.Escape(value)
		}
	}

	if at != nil {
		body["attach"] = at.Data
		body["suffix"] = strings.TrimSpace(suffix)
	}
	if len(body) == 0 {
		t.Header(t.M["null_article"], "", nil, true)
		t.Footer(nil)
		return nil, errors.New("null article")
	}
	stamp := time.Now().Unix()
	if t.Req.FormValue("error") != "" {
		stamp = t.errorTime()
	}
	rec := record.New(ca.Datfile, "", 0)
	passwd := t.Req.FormValue("passwd")
	rec.Build(stamp, body, passwd)
	return rec, nil
}

//doPost parses multipart form ,makes record of it and adds to cache.
//if form dopost=yes broadcasts it.
func (t *threadCGI) doPost() string {
	attached, attachedErr := t.parseAttached()
	if attachedErr != nil {
		log.Println(attachedErr)
	}
	suffix := t.guessSuffix(attached)
	ca := thread.NewCache(t.Req.FormValue("file"))
	rec, err := t.makeRecord(attached, suffix, ca)
	if err != nil {
		return ""
	}
	proxyClient := t.Req.Header.Get("X_FORWARDED_FOR")
	log.Printf("post %s/%d_%s from %s/%s\n", ca.Datfile, ca.Stamp(), rec.ID, t.Req.RemoteAddr, proxyClient)

	if len(rec.Recstr()) > cfg.RecordLimit<<10 {
		t.Header(t.M["big_file"], "", nil, true)
		t.Footer(nil)
		return ""
	}
	if rec.IsSpam() {
		t.Header(t.M["spam"], "", nil, true)
		t.Footer(nil)
		return ""
	}

	if ca.Exists() {
		rec.Sync()
	} else {
		t.Print404(nil, "")
		return ""
	}

	if t.Req.FormValue("dopost") != "" {
		log.Println(rec.Datfile, rec.ID, "is queued")
		go updateque.UpdateNodes(rec, nil)
	}

	return rec.ID[:8]

}

//attached represents attached file name and contents.
type attached struct {
	Filename string
	Data     string
}

//parseAttached reads attached file and returns attached obj.
//if size>recordLimit renders error page.
func (t *threadCGI) parseAttached() (*attached, error) {
	err := t.Req.ParseMultipartForm(int64(cfg.RecordLimit) << 10)
	if err != nil {
		return nil, err
	}
	attach := t.Req.MultipartForm
	if len(attach.File) == 0 {
		return nil, errors.New("attached file not found")
	}
	var fpStrAttach *multipart.FileHeader
	for _, v := range attach.File {
		fpStrAttach = v[0]
	}
	f, err := fpStrAttach.Open()
	defer util.Fclose(f)
	if err != nil {
		log.Println(err)
		return nil, err
	}
	var strAttach = make([]byte, cfg.RecordLimit<<10)
	s, err := f.Read(strAttach)
	if s > cfg.RecordLimit<<10 {
		log.Println("attached file is too big")
		t.Header(t.M["big_file"], "", nil, true)
		t.Footer(nil)
		return nil, err
	}
	if err != nil {
		log.Println(err)
		return nil, err
	}
	coded := base64.StdEncoding.EncodeToString(strAttach[:s])
	return &attached{
		fpStrAttach.Filename,
		coded,
	}, nil
}
