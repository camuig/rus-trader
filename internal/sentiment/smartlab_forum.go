package sentiment

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"golang.org/x/net/html"
)

type ForumPost struct {
	Title string
}

func FetchForumPosts(ctx context.Context, ticker string, maxPosts int) ([]ForumPost, error) {
	url := fmt.Sprintf("https://smart-lab.ru/forum/%s/", ticker)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 rus-trader/1.0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	doc, err := html.Parse(resp.Body)
	if err != nil {
		return nil, err
	}

	return parseForumPosts(doc, maxPosts), nil
}

func parseForumPosts(doc *html.Node, maxPosts int) []ForumPost {
	var posts []ForumPost
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			for _, a := range n.Attr {
				if a.Key == "href" && strings.Contains(a.Val, "/blog/") {
					title := extractNodeText(n)
					title = strings.TrimSpace(title)
					if title != "" && len(posts) < maxPosts {
						posts = append(posts, ForumPost{Title: title})
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return posts
}

func extractNodeText(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			sb.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return sb.String()
}
