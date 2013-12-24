package main

import (
	"code.google.com/p/go.crypto/scrypt"
	"crypto/rand"
	"encoding/xml"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"time"
)

var client *http.Client

func init() {
	client = &http.Client{}
}

func CreateUser(name string, password string) (userID int32, err error) {
	salt := make([]byte, 8)
	_, _ = rand.Read(salt)

	var digest []byte
	digest, err = scrypt.Key([]byte(password), salt, 16384, 8, 1, 32)
	if err != nil {
		return
	}

	return repo.createUser(name, digest, salt)
}

func Subscribe(userID int32, feedURL string) (err error) {
	feedID, err := repo.getFeedIDByURL(feedURL)
	if err == notFound {
		feedID, err = repo.createFeed(feedURL, feedURL)
		if err != nil {
			return err
		}
	}
	repo.createSubscription(userID, feedID)
	if err != nil {
		return err
	}

	return nil
}

func KeepFeedsFresh() {
	for {
		t := time.Now().Add(-10 * time.Minute)
		if staleFeeds, err := repo.getFeedsUncheckedSince(t); err == nil {
			for _, sf := range staleFeeds {
				RefreshFeed(sf)
			}
		}
		time.Sleep(time.Minute)
	}
}

type rawFeed struct {
	url  string
	body []byte
	etag string
}

func fetchFeed(url string) (feed *rawFeed, err error) {
	feed = &rawFeed{url: url}

	var resp *http.Response
	resp, err = http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("Bad HTTP response: %s", resp.Status)
	}

	feed.body, err = ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("Unable to read response body: %v", err)
	}

	feed.etag = resp.Header.Get("Etag")

	return feed, nil
}

func RefreshFeed(staleFeed staleFeed) {
	rawFeed, err := fetchFeed(staleFeed.url)
	if err != nil {
		repo.updateFeedWithFetchFailure(staleFeed.id, err.Error(), time.Now())
		return
	}

	feed, err := parseFeed(rawFeed.body)
	if err != nil {
		repo.updateFeedWithFetchFailure(staleFeed.id, fmt.Sprintf("Unable to parse feed: %v", err), time.Now())
		return
	}

	repo.updateFeedWithFetchSuccess(staleFeed.id, feed, rawFeed.etag, time.Now())
}

// func fetchFeed(url string, etag string) (body string, err error) {
// 	var req *http.Request
// 	var resp *http.Response

// 	req, err = http.NewRequest("GET", url, nil)
// 	if err != nil {
// 		return err
// 	}

// 	if etag != "" {
// 		req.Header.Add("If-None-Match", "etag")
// 	}

// 	resp, err = client.Do(req)
// 	defer resp.Body.Close()
// 	if err != nil {
// 		return err
// 	}

// 	return
// }

// type fetchedFeed struct {
//         name string
//       url string
//       time time.Time
//       etag string
//       last_failure varchar,
//       last_failure_time timestamp with time zone,
// }

type parsedItem struct {
	url             string
	title           string
	publicationTime time.Time
}

func (i *parsedItem) isValid() bool {
	var zeroTime time.Time

	return i.url != "" && i.title != "" && i.publicationTime != zeroTime
}

type parsedFeed struct {
	name  string
	items []parsedItem
}

func (f *parsedFeed) isValid() bool {
	if f.name == "" {
		return false
	}

	for _, item := range f.items {
		if !item.isValid() {
			return false
		}
	}

	return true
}

func parseFeed(body []byte) (f *parsedFeed, err error) {
	f, err = parseRSS(body)
	if err == nil {
		return f, nil
	}

	return parseAtom(body)
}

func parseRSS(body []byte) (*parsedFeed, error) {
	type Item struct {
		Link    string `xml:"link"`
		Title   string `xml:"title"`
		Date    string `xml:"date"`
		PubDate string `xml:"pubDate"`
	}

	type Channel struct {
		Title string `xml:"title"`
		Item  []Item `xml:"item"`
	}

	var rss struct {
		Channel Channel `xml:"channel"`
	}

	err := xml.Unmarshal(body, &rss)
	if err != nil {
		return nil, err
	}

	var feed parsedFeed
	feed.name = rss.Channel.Title
	feed.items = make([]parsedItem, len(rss.Channel.Item))
	for i, item := range rss.Channel.Item {
		feed.items[i].url = item.Link
		feed.items[i].title = item.Title
		if item.Date != "" {
			feed.items[i].publicationTime, _ = parseTime(item.Date)
		}
		if item.PubDate != "" {
			feed.items[i].publicationTime, _ = parseTime(item.PubDate)
		}
	}

	if !feed.isValid() {
		return nil, errors.New("Invalid RSS")
	}

	return &feed, nil
}

func parseAtom(body []byte) (*parsedFeed, error) {
	type Link struct {
		Href string `xml:"href,attr"`
	}

	type Entry struct {
		Link      Link   `xml:"link"`
		Title     string `xml:"title"`
		Published string `xml:"published"`
		Updated   string `xml:"updated"`
	}

	var atom struct {
		Title string  `xml:"title"`
		Entry []Entry `xml:"entry"`
	}

	err := xml.Unmarshal(body, &atom)
	if err != nil {
		return nil, err
	}

	var feed parsedFeed
	feed.name = atom.Title
	feed.items = make([]parsedItem, len(atom.Entry))
	for i, entry := range atom.Entry {
		feed.items[i].url = entry.Link.Href
		feed.items[i].title = entry.Title
		if entry.Published != "" {
			feed.items[i].publicationTime, _ = parseTime(entry.Published)
		}
		if entry.Updated != "" {
			feed.items[i].publicationTime, _ = parseTime(entry.Updated)
		}
	}

	if !feed.isValid() {
		return nil, errors.New("Invalid Atom")
	}

	return &feed, nil
}

// Try multiple time formats one after another until one works or all fail
func parseTime(value string) (t time.Time, err error) {
	formats := []string{
		"2006-01-02T15:04:05-07:00",
		"2006-01-02T15:04:05Z",
		time.RFC822,
		"02 Jan 2006 15:04 MST",    // RFC822 with 4 digit year
		"02 Jan 2006 15:04:05 MST", // RFC822 with 4 digit year and seconds
		time.RFC1123,
		time.RFC1123Z,
	}
	for _, f := range formats {
		t, err = time.Parse(f, value)
		if err == nil {
			return
		}
	}

	return t, errors.New("Unable to parse time")
}
