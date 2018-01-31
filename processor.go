package wechat_spider

import (
	"bytes"
	"crypto/md5"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/elazarl/goproxy"
	"github.com/palantir/stacktrace"
)

type Processor interface {
	ProcessList(resp *http.Response, ctx *goproxy.ProxyCtx) ([]byte, error)
	ProcessDetail(resp *http.Response, ctx *goproxy.ProxyCtx) ([]byte, error)
	ProcessMetrics(resp *http.Response, ctx *goproxy.ProxyCtx) ([]byte, error)
	NextBiz(currentBiz string) string
	HistoryUrl() string
	Output()
	NextUrl(currentUrl string) string
}

type BaseProcessor struct {
	req          *http.Request
	lastId       string
	offset       int
	data         []byte
	urlResults   []*UrlResult
	detailResult *DetailResult
	historyUrl   string
	biz          string
	token        string
	// The index of urls for detail page
	currentIndex int

	Type string
}

type (
	UrlResult struct {
		Mid string
		// url
		Url  string
		_URL *url.URL
	}
	DetailResult struct {
		Id         string
		Url        string
		Data       []byte
		Appmsgstat *MsgStat `json:"appmsgstat"`
	}
	MsgStat struct {
		ReadNum     int `json:"read_num"`
		LikeNum     int `json:"like_num"`
		RealReadNum int `json:"real_read_num"`
	}
)

var (
	replacer = strings.NewReplacer(
		"\t", "", " ", "",
		"&quot;", `"`, "&nbsp;", "",
		`\\`, "", "&amp;amp;", "&",
		"&amp;", "&", `\`, "",
	)
	urlRegex    = regexp.MustCompile(`http://mp.weixin.qq.com/s?[^#"',]*`)
	idRegex     = regexp.MustCompile(`"id":(\d+)`)
	tokenRegex  = regexp.MustCompile(`"(.*)"`)
	MsgNotFound = errors.New("MsgLists not found")

	TypeList   = "list"
	TypeDetail = "detail"
	TypeMetric = "metric"
)

func NewBaseProcessor() *BaseProcessor {
	return &BaseProcessor{}
}

func (p *BaseProcessor) init(req *http.Request, data []byte) (err error) {
	p.req = req
	p.data = data
	p.currentIndex = -1
	p.biz = req.URL.Query().Get("__biz")
	p.historyUrl = req.URL.String()
	fmt.Println("Running a new wechat processor, please wait...")
	return nil
}
func (p *BaseProcessor) ProcessList(resp *http.Response, ctx *goproxy.ProxyCtx) (data []byte, err error) {
	p.Type = TypeList
	var buf bytes.Buffer
	if _, err = buf.ReadFrom(resp.Body); err != nil {
		return
	}
	if err = resp.Body.Close(); err != nil {
		return
	}

	data = buf.Bytes()
	if err = p.init(ctx.Req, data); err != nil {
		return
	}

	if err = p.processMain(); err != nil {
		return
	}

	// if ctx.Req.URL.Path == `/mp/profile_ext` && (ctx.Req.URL.Query().Get("action") == "home" || ctx.Req.URL.Query().Get("action") == "getmsg")
	if rootConfig.AutoScroll {
		if err = p.processPages(); err != nil {
			return
		}
	}
	return
}

func (p *BaseProcessor) ProcessDetail(resp *http.Response, ctx *goproxy.ProxyCtx) (data []byte, err error) {
	p.Type = TypeDetail
	p.req = ctx.Req
	p.currentIndex++
	var buf bytes.Buffer
	if _, err = buf.ReadFrom(resp.Body); err != nil {
		return
	}
	if err = resp.Body.Close(); err != nil {
		return
	}
	// p.logf("process detail.......")
	data = buf.Bytes()
	p.detailResult = &DetailResult{Id: genId(p.req.URL.String()), Url: p.req.URL.String(), Data: data}
	return
}

func (p *BaseProcessor) ProcessMetrics(resp *http.Response, ctx *goproxy.ProxyCtx) (data []byte, err error) {
	p.Type = TypeMetric
	p.req = ctx.Req

	var buf bytes.Buffer
	if _, err = buf.ReadFrom(resp.Body); err != nil {
		return
	}
	if err = resp.Body.Close(); err != nil {
		return
	}
	data = buf.Bytes()
	str := buf.String()
	p.logf("stat===========%v", data)
	p.logf("string===========%s", str)
	detailResult := &DetailResult{}
	e := json.Unmarshal(data, detailResult)
	if e != nil {
		p.logf("error in parsing json %s\n", string(data))
	}
	detailResult.Url = p.req.Referer()
	detailResult.Id = genId(detailResult.Url)
	p.detailResult = detailResult

	return
}

func (p *BaseProcessor) NextBiz(currentBiz string) string {
	return ""
}

func (p *BaseProcessor) NextUrl(currentUrl string) string {
	return ""
}

func (p *BaseProcessor) HistoryUrl() string {
	return p.historyUrl
}

func (p *BaseProcessor) Sleep() {
	time.Sleep(50 * time.Millisecond)
}

func (p *BaseProcessor) UrlResults() []*UrlResult {
	return p.urlResults
}

func (p *BaseProcessor) DetailResult() *DetailResult {
	return p.detailResult
}

func (p *BaseProcessor) GetRequest() *http.Request {
	return p.req
}

func (p *BaseProcessor) Output() {
	urls := []string{}
	fmt.Println("result => [")
	for _, r := range p.urlResults {
		urls = append(urls, r.Url)
	}
	fmt.Println(strings.Join(urls, ","))
	fmt.Println("]")

	//同时打印进入详情页面，URL 点赞
	fmt.Println("\n1111=============\n")
	// fmt.Printf("url %s %s is being spidered\n", p.DetailResult().Id, p.DetailResult().Url)
	fmt.Printf("\n2222=============\n")
	// fmt.Printf("url %s %s metric %#v is being spidered\n", p.DetailResult().Id, p.DetailResult().Url, p.DetailResult().Appmsgstat)
	fmt.Printf("\n3333=============\n")
}

//Parse the html
func (p *BaseProcessor) processMain() error {
	p.urlResults = make([]*UrlResult, 0, 100)
	buffer := bytes.NewBuffer(p.data)
	var msgs string
	var token string
	str, err := buffer.ReadString('\n')
	// general_msg_list
	if strings.Contains(str, "general_msg_list") {
		msgs = str
	} else {
		for err == nil {
			if strings.Contains(str, "window.appmsg_token =") {
				token = str
			}
			// p.logf("str-----%s", str)
			if strings.Contains(str, "msgList = ") {
				msgs = str
				break
			}
			str, err = buffer.ReadString('\n')
		}
	}
	if token != "" {
		p.logf("str-----token-----%s", token)
		tokenMatcher := tokenRegex.FindAllStringSubmatch(token, -1)
		if len(tokenMatcher) >= 1 {
			p.token = tokenMatcher[0][1]
			p.logf("str-----p.token-----%s", p.token)
		}
	}

	if msgs == "" {
		return stacktrace.Propagate(MsgNotFound, "Failed parse main")
	}
	msgs = replacer.Replace(msgs)
	urls := urlRegex.FindAllString(msgs, -1)
	if len(urls) < 1 {
		return stacktrace.Propagate(MsgNotFound, "Failed find url in  main")
	}
	p.urlResults = make([]*UrlResult, len(urls))
	for i, u := range urls {
		p.urlResults[i] = &UrlResult{Url: u}
	}

	idMatcher := idRegex.FindAllStringSubmatch(msgs, -1)
	if len(idMatcher) < 1 {
		return stacktrace.Propagate(MsgNotFound, "Failed find id in  main")
	}
	// p.logf("idMatcher length................ %d", len(idMatcher))
	p.lastId = idMatcher[len(idMatcher)-1][1]
	p.offset = p.offset + len(idMatcher) + 1
	return nil
}

func (p *BaseProcessor) processPages() (err error) {
	var pageUrl = p.genPageUrl()
	// p.logf("process pages....====================%s", pageUrl)
	req, err := http.NewRequest("GET", pageUrl, nil)
	if err != nil {
		return stacktrace.Propagate(err, "Failed new page request")
	}
	for k, _ := range p.req.Header {
		req.Header.Set(k, p.req.Header.Get(k))
	}
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return stacktrace.Propagate(err, "Failed get page response")
	}
	bs, _ := ioutil.ReadAll(resp.Body)
	defer resp.Body.Close()
	str := replacer.Replace(string(bs))
	result := urlRegex.FindAllString(str, -1)
	if len(result) < 1 {
		return stacktrace.Propagate(err, "Failed get page url")
	}
	idMatcher := idRegex.FindAllStringSubmatch(str, -1)
	if len(idMatcher) < 1 {
		return stacktrace.Propagate(err, "Failed get page id")
	}
	p.lastId = idMatcher[len(idMatcher)-1][1]
	p.offset = p.offset + len(idMatcher) + 1
	p.logf("Page Get => %d,lastid: %s, offset: %d", len(result), p.lastId, p.offset)
	for _, u := range result {
		p.urlResults = append(p.urlResults, &UrlResult{Url: u})
	}
	if p.lastId != "" {
		p.Sleep()
		return p.processPages()
	}
	return nil
}

func (p *BaseProcessor) genPageUrl() string {

	a := p.req.URL.Query()
	a.Set("offset", strconv.Itoa(p.offset))
	a.Set("action", "getmsg")
	a.Set("count", "10")
	a.Set("f", "json")
	a.Set("is_ok", "1")
	a.Set("uin", "777")
	a.Set("key", "777")

	a.Set("appmsg_token", p.token)

	a.Set("x5", "1")

	rawUrl := a.Encode()
	urlStr := "http://mp.weixin.qq.com/mp/profile_ext?" + rawUrl

	p.logf("pageUrl+++++++++++++++%s, offset:=======%d", urlStr, p.offset)

	return urlStr
}

func genId(urlStr string) string {
	uri, _ := url.ParseRequestURI(urlStr)
	return hashKey(uri.Query().Get("__biz") + "_" + uri.Query().Get("mid") + "_" + uri.Query().Get("idx"))
}

func hashKey(key string) string {
	h := md5.New()
	io.WriteString(h, key)
	return fmt.Sprintf("%x", h.Sum(nil))
}

func (P *BaseProcessor) logf(format string, msg ...interface{}) {
	if rootConfig.Verbose {
		Logger.Printf(format, msg...)
	}
}
