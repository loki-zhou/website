package main

import (
	"bbs/cfg"
	"bbs/db"
	"bbs/gou"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	fmt.Println("starting Gou", cfg.Version, "...")
	var printLog, isSilent bool
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "P2P anonymous BBS shinGETsu Gou %s\n", cfg.Version)
		fmt.Fprintf(os.Stderr, "%s <options>\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.BoolVar(&printLog, "verbose", false, "print logs")
	flag.BoolVar(&printLog, "v", false, "print logs")
	flag.BoolVar(&isSilent, "silent", false, "suppress logs")
	flag.Parse()
	cfg.Parse()
	gou.SetupDirectories()
	gou.SetLogger(printLog, isSilent)
	log.Println("********************starting Gou", cfg.Version, "...******************")
	gou.ExpandAssets()
	db.Setup()
	listener, ch := gou.StartDaemon()
	c := make(chan os.Signal)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		for range c {
			fmt.Println("exiting...")
			if err := listener.Close(); err != nil {
				log.Println(err)
			}
			if err := db.DB.Close(); err != nil {
				log.Println(err)
			}
		}
	}()
	log.Println(<-ch)
}