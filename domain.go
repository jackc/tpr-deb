package main

import (
	"code.google.com/p/go.crypto/scrypt"
	"crypto/rand"
	"encoding/xml"
	"errors"
	"fmt"
	"github.com/JackC/pgx"
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

	var v interface{}
	v, err = pool.SelectValue("insert into users(name, password_digest, password_salt) values($1, $2, $3) returning id", name, digest, salt)
	if err != nil {
		return
	}
	userID = v.(int32)

	return
}

func Subscribe(userID int32, feedURL string) (err error) {
	var feedID interface{}
	feedID, err = pool.SelectValue("select id from feeds where url=$1", feedURL)
	if _, ok := err.(pgx.NotSingleRowError); ok {
		var resp *http.Response
		resp, err = http.Get(feedURL)
		if err != nil {
			return err
		}
		if resp.StatusCode != 200 {
			return fmt.Errorf("Bad HTTP response: %s", resp.Status)
		}
		var body []byte
		body, err = ioutil.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("Unable to read response body: %v", err)
		}

		var feed *parsedFeed
		feed, err = parseFeed(body)
		if err != nil {
			return fmt.Errorf("Unable to parse feed: %v", err)
		}

		var conn *pgx.Connection
		conn, err = pool.Acquire()
		if err != nil {
			return err
		}
		defer pool.Release(conn)

		committed, txErr := conn.Transaction(func() bool {
			feedID, err = conn.SelectValue("insert into feeds(name, url, last_fetch_time, etag) values($1, $2, now(), $3) returning id", feed.name, feedURL, resp.Header.Get("Etag"))
			if err != nil {
				return false
			}

			for _, item := range feed.items {
				_, err = conn.Execute("insert into items(feed_id, url, title, body, publication_time) values($1, $2, $3, $4, $5)", feedID, item.url, item.title, item.body, item.publicationTime)
				if err != nil {
					return false
				}
			}

			_, err = conn.Execute("insert into subscriptions(user_id, feed_id) values($1, $2)", userID, feedID)
			if err != nil {
				return false
			}

			return true
		})
		if err != nil {
			return err
		}
		if txErr != nil {
			return err
		}
		if !committed {
			return errors.New("Commit failed")
		}

		return nil
	}
	if err != nil {
		return err
	}

	_, err = pool.Execute("insert into subscriptions(user_id, feed_id) values($1, $2)", userID, feedID)
	if err != nil {
		return err
	}
	return
}

type feedIndexFeed struct {
	id   int32
	name string
	url  string
}

func GetFeedsForUserID(userID int32) (feeds []feedIndexFeed, err error) {
	err = pool.SelectFunc("select id, name, url from feeds join subscriptions on feeds.id=subscriptions.feed_id where user_id=$1 order by name", func(r *pgx.DataRowReader) (err error) {
		var feed feedIndexFeed
		feed.id = r.ReadValue().(int32)
		feed.name = r.ReadValue().(string)
		feed.url = r.ReadValue().(string)
		feeds = append(feeds, feed)
		return
	}, userID)

	return
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
	body            string
	publicationTime time.Time
}

func (i *parsedItem) isValid() bool {
	var zeroTime time.Time

	return i.url != "" && i.title != "" && i.body != "" && i.publicationTime != zeroTime
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
		Link        string `xml:"link"`
		Title       string `xml:"title"`
		Description string `xml:"description"`
		Date        string `xml:"date"`
		PubDate     string `xml:"pubDate"`
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
		feed.items[i].body = item.Description
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
		Content   string `xml:"content"`
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
		feed.items[i].body = entry.Content
		if entry.Published != "" {
			feed.items[i].publicationTime, _ = parseTime(entry.Published)
		}
		if entry.Updated != "" {
			feed.items[i].publicationTime, _ = parseTime(entry.Updated)
		}
	}

	fmt.Println(feed.name)

	for _, item := range feed.items {
		fmt.Println(item.url)
		fmt.Println(item.title)
		fmt.Println(len(item.body))
		fmt.Println(item.publicationTime)
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
