package main

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/go-echarts/go-echarts/v2/charts"
	"github.com/go-echarts/go-echarts/v2/opts"
	"github.com/go-echarts/go-echarts/v2/types"
	"github.com/patrickmn/go-cache"
	"golang.org/x/time/rate"
)

// Page represents a crawled web page
type Page struct {
	URL      string
	Title    string
	Links    []string
	Depth    int
	Parent   string
	Status   int
	Error    error
	Duration time.Duration
}

// Crawler manages the web crawling process
type Crawler struct {
	client        *http.Client
	limiter       *rate.Limiter
	visited       *sync.Map
	cache         *cache.Cache
	pages         chan Page
	wg            *sync.WaitGroup
	baseURL       string
	maxDepth      int
	allowExternal bool
	userAgent     string
	concurrency   int
	timeout       time.Duration
	robots        map[string][]string
	mutex         *sync.Mutex
}

// NewCrawler creates a new crawler instance
func NewCrawler(opts ...CrawlerOption) *Crawler {
	c := &Crawler{
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
		limiter:       rate.NewLimiter(rate.Every(time.Second), 10), // 10 requests per second
		visited:       &sync.Map{},
		cache:         cache.New(5*time.Minute, 10*time.Minute),
		pages:         make(chan Page, 100),
		wg:            &sync.WaitGroup{},
		maxDepth:      3,
		allowExternal: false,
		userAgent:     "GoCrawler/1.0",
		concurrency:   5,
		timeout:       10 * time.Second,
		robots:        make(map[string][]string),
		mutex:         &sync.Mutex{},
	}

	// Apply options
	for _, opt := range opts {
		opt(c)
	}

	return c
}

// CrawlerOption is a function that configures a Crawler
type CrawlerOption func(*Crawler)

// WithConcurrency sets the concurrency level
func WithConcurrency(n int) CrawlerOption {
	return func(c *Crawler) {
		if n > 0 {
			c.concurrency = n
		}
	}
}

// WithMaxDepth sets the maximum crawl depth
func WithMaxDepth(d int) CrawlerOption {
	return func(c *Crawler) {
		if d > 0 {
			c.maxDepth = d
		}
	}
}

// WithUserAgent sets the user agent string
func WithUserAgent(ua string) CrawlerOption {
	return func(c *Crawler) {
		if ua != "" {
			c.userAgent = ua
		}
	}
}

// WithTimeout sets the HTTP client timeout
func WithTimeout(t time.Duration) CrawlerOption {
	return func(c *Crawler) {
		if t > 0 {
			c.timeout = t
			c.client.Timeout = t
		}
	}
}

// WithRateLimit sets the rate limiter
func WithRateLimit(r time.Duration, burst int) CrawlerOption {
	return func(c *Crawler) {
		if r > 0 && burst > 0 {
			c.limiter = rate.NewLimiter(rate.Every(r), burst)
		}
	}
}

// WithAllowExternal configures whether to crawl external domains
func WithAllowExternal(allow bool) CrawlerOption {
	return func(c *Crawler) {
		c.allowExternal = allow
	}
}

// Start begins the crawling process
func (c *Crawler) Start(startURL string) {
	parsedURL, err := url.Parse(startURL)
	if err != nil {
		log.Fatalf("Invalid URL: %v", err)
	}
	c.baseURL = fmt.Sprintf("%s://%s", parsedURL.Scheme, parsedURL.Host)

	// Load robots.txt
	c.loadRobotsTxt(c.baseURL)

	// Create worker pool
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start the result collector
	resultChan := make(chan Page, 100)
	go c.collectResults(resultChan)

	// Add initial job to queue
	c.wg.Add(1)
	go c.crawl(ctx, startURL, 0, "", resultChan)

	// Wait for all tasks to complete
	c.wg.Wait()
	close(resultChan)
}

func (c *Crawler) crawl(ctx context.Context, pageURL string, depth int, parent string, results chan<- Page) {
	defer c.wg.Done()

	// Check context cancellation
	select {
	case <-ctx.Done():
		return
	default:
	}

	// Skip if max depth reached
	if depth > c.maxDepth {
		return
	}

	// Skip if already visited
	urlHash := hash(pageURL)
	if _, visited := c.visited.LoadOrStore(urlHash, true); visited {
		return
	}

	// Check if URL is allowed by robots.txt
	if !c.isAllowed(pageURL) {
		return
	}

	// Check if the URL is external and if we should skip it
	if !c.allowExternal && !c.isSameDomain(pageURL) {
		return
	}

	// Check cache first
	if cachedPage, found := c.cache.Get(urlHash); found {
		results <- cachedPage.(Page)
		return
	}

	// Wait for rate limiter
	err := c.limiter.Wait(ctx)
	if err != nil {
		return
	}

	// Create request
	req, err := http.NewRequestWithContext(ctx, "GET", pageURL, nil)
	if err != nil {
		results <- Page{URL: pageURL, Parent: parent, Depth: depth, Error: err}
		return
	}
	req.Header.Set("User-Agent", c.userAgent)

	// Execute request
	start := time.Now()
	resp, err := c.client.Do(req)
	duration := time.Since(start)

	page := Page{
		URL:      pageURL,
		Parent:   parent,
		Depth:    depth,
		Duration: duration,
	}

	if err != nil {
		page.Error = err
		results <- page
		return
	}
	defer resp.Body.Close()

	page.Status = resp.StatusCode

	// Skip non-successful responses
	if resp.StatusCode != http.StatusOK {
		results <- page
		return
	}

	// Check content type
	contentType := resp.Header.Get("Content-Type")
	if !strings.Contains(contentType, "text/html") {
		results <- page
		return
	}

	// Parse HTML
	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		page.Error = err
		results <- page
		return
	}

	// Extract title
	page.Title = strings.TrimSpace(doc.Find("title").Text())

	// Extract links
	links := make([]string, 0)
	doc.Find("a[href]").Each(func(_ int, s *goquery.Selection) {
		if href, exists := s.Attr("href"); exists {
			absoluteURL := c.normalizeURL(pageURL, href)
			if absoluteURL != "" {
				links = append(links, absoluteURL)
			}
		}
	})
	page.Links = links

	// Cache page
	c.cache.Set(urlHash, page, cache.DefaultExpiration)

	// Send result
	results <- page

	// Schedule crawling for found links
	if depth < c.maxDepth {
		for _, link := range links {
			// Check if already visited before adding to queue
			linkHash := hash(link)
			if _, visited := c.visited.LoadOrStore(linkHash, true); !visited {
				c.wg.Add(1)
				go c.crawl(ctx, link, depth+1, pageURL, results)
			}
		}
	}
}

func (c *Crawler) collectResults(results <-chan Page) {
	var pages []Page
	for page := range results {
		pages = append(pages, page)
		log.Printf("[%d] Crawled: %s (depth: %d, links: %d)", page.Status, page.URL, page.Depth, len(page.Links))
	}

	// Generate visualization
	c.generateVisualization(pages)
}

func (c *Crawler) normalizeURL(baseURL, href string) string {
	// Skip fragment-only URLs, javascript, mailto, etc.
	if href == "" || strings.HasPrefix(href, "#") || strings.HasPrefix(href, "javascript:") || strings.HasPrefix(href, "mailto:") {
		return ""
	}

	// Parse base URL
	base, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}

	// Parse target URL
	target, err := url.Parse(href)
	if err != nil {
		return ""
	}

	// Resolve relative URLs
	resolvedURL := base.ResolveReference(target)

	// Normalize
	resolvedURL.Fragment = "" // Remove fragments
	return resolvedURL.String()
}

func (c *Crawler) isSameDomain(pageURL string) bool {
	parsedURL, err := url.Parse(pageURL)
	if err != nil {
		return false
	}
	baseHost := fmt.Sprintf("%s://%s", parsedURL.Scheme, parsedURL.Host)
	return baseHost == c.baseURL
}

func (c *Crawler) loadRobotsTxt(baseURL string) {
	robotsURL := fmt.Sprintf("%s/robots.txt", baseURL)
	resp, err := http.Get(robotsURL)
	if err != nil || resp.StatusCode != http.StatusOK {
		return // No robots.txt or couldn't fetch it
	}
	defer resp.Body.Close()

	var disallowPaths []string
	scanner := regexp.MustCompile(`(?i)Disallow:\s*([^#\s]+)`)
	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return
	}

	// Extract robots.txt content
	robotsTxt := doc.Text()
	matches := scanner.FindAllStringSubmatch(robotsTxt, -1)
	for _, match := range matches {
		if len(match) > 1 {
			disallowPaths = append(disallowPaths, match[1])
		}
	}

	c.mutex.Lock()
	c.robots[baseURL] = disallowPaths
	c.mutex.Unlock()
}

func (c *Crawler) isAllowed(pageURL string) bool {
	parsedURL, err := url.Parse(pageURL)
	if err != nil {
		return false
	}

	baseHost := fmt.Sprintf("%s://%s", parsedURL.Scheme, parsedURL.Host)
	path := parsedURL.Path

	c.mutex.Lock()
	disallowPaths, ok := c.robots[baseHost]
	c.mutex.Unlock()

	if !ok {
		return true // No rules for this host
	}

	for _, disallowPath := range disallowPaths {
		if strings.HasPrefix(path, disallowPath) {
			return false
		}
	}
	return true
}

func (c *Crawler) generateVisualization(pages []Page) {
	// Create a network graph
	graph := charts.NewGraph()
	graph.SetGlobalOptions(
		charts.WithTitleOpts(opts.Title{
			Title: "Web Crawler Results",
		}),
		charts.WithInitializationOpts(opts.Initialization{
			Width:  "1000px",
			Height: "800px",
			Theme:  types.ThemeWesteros,
		}),
		charts.WithLegendOpts(opts.Legend{
			Show: true,
		}),
	)

	// Create nodes and links
	nodes := make([]opts.GraphNode, 0)
	links := make([]opts.GraphLink, 0)
	nodeMap := make(map[string]int)

	// Add nodes
	for i, page := range pages {
		category := 0
		if page.Status >= 400 {
			category = 1
		} else if !c.isSameDomain(page.URL) {
			category = 2
		}

		name := page.URL
		if len(name) > 30 {
			name = name[:30] + "..."
		}

		nodes = append(nodes, opts.GraphNode{
			Name:     name,
			Category: category,
			Value:    len(page.Links),
		})

		nodeMap[page.URL] = i
	}

	// Add links
	for _, page := range pages {
		if sourceIdx, ok := nodeMap[page.URL]; ok {
			for _, link := range page.Links {
				if targetIdx, ok := nodeMap[link]; ok {
					links = append(links, opts.GraphLink{
						Source: sourceIdx,
						Target: targetIdx,
					})
				}
			}
		}
	}

	// Add categories
	categories := []opts.GraphCategory{
		{Name: "Normal"},
		{Name: "Error"},
		{Name: "External"},
	}

	// Set data
	graph.AddSeries("graph", nodes, links,
		charts.WithGraphChartOpts(opts.GraphChart{
			Layout:             "force",
			Roam:               true,
			FocusNodeAdjacency: true,
			Categories:         categories,
			Force: &opts.GraphForce{
				RepulsionRatio: 80.0,
				Gravity:        0.1,
				EdgeLength:     100,
			},
		}),
	)

	// Output HTML file
	f, err := os.Create("crawler_results.html")
	if err != nil {
		log.Println("Error creating visualization file:", err)
		return
	}
	defer f.Close()

	err = graph.Render(f)
	if err != nil {
		log.Println("Error rendering graph:", err)
		return
	}

	log.Println("Visualization generated: crawler_results.html")
}

// Helper functions
func hash(s string) string {
	hasher := md5.New()
	hasher.Write([]byte(s))
	return hex.EncodeToString(hasher.Sum(nil))
}

func main() {
	url := flag.String("url", "", "URL to start crawling from")
	depth := flag.Int("depth", 3, "Maximum crawl depth")
	concurrency := flag.Int("concurrency", 5, "Number of concurrent crawlers")
	rateLimit := flag.Int("rate", 10, "Rate limit (requests per second)")
	timeout := flag.Int("timeout", 10, "HTTP request timeout in seconds")
	allowExternal := flag.Bool("external", false, "Allow crawling external domains")
	userAgent := flag.String("agent", "GoCrawler/1.0", "User agent string")

	flag.Parse()

	if *url == "" {
		log.Fatal("URL is required. Use -url flag.")
	}

	// Create and configure crawler
	crawler := NewCrawler(
		WithMaxDepth(*depth),
		WithConcurrency(*concurrency),
		WithRateLimit(time.Second/time.Duration(*rateLimit), *rateLimit),
		WithTimeout(time.Duration(*timeout)*time.Second),
		WithAllowExternal(*allowExternal),
		WithUserAgent(*userAgent),
	)

	// Start crawling
	log.Printf("Starting crawl from %s with depth %d", *url, *depth)
	startTime := time.Now()
	crawler.Start(*url)
	log.Printf("Crawl completed in %s", time.Since(startTime))
}
