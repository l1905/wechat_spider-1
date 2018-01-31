package main

import (
	spider "github.com/l1905/wechat_spider-1"
)

func main() {
	var port = "8899"
	spider.InitConfig(&spider.Config{
		Verbose:    true,  // Open to see detail logs
		AutoScroll: false, // Open to crawl scroll pages
		Compress:   true,  // Ingore other request to save the
	})
	spider.Regist(spider.NewBaseProcessor())
	spider.Run(port)
}
