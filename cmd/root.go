/*
Copyright © 2021 Michael Eder @edermi / twitter.com/michael_eder_

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU General Public License for more details.

You should have received a copy of the GNU General Public License
along with this program. If not, see <http://www.gnu.org/licenses/>.
*/
package cmd

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
	"unicode"

	"github.com/gocolly/colly"
	"github.com/spf13/cobra"
	"golang.org/x/net/html"
)

type skweezConf struct {
	debug      bool
	depth      int
	minLen     int
	maxLen     int
	scope      []string
	output     string
	noFilter   bool
	jsonOutput bool
	targets    []string
	urlFilter  []*regexp.Regexp
}

var validWordRegex = regexp.MustCompile(`^[a-zA-Z0-9]+.*[a-zA-Z0-9]$`)
var stripTrailingSymbols = "!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~"

var rootCmd = &cobra.Command{
	Use:   "skweez domain1 domain2 domain3",
	Short: "Sqeezes the words out of websites",
	Long: `skweez is a fast and easy to use tool that allows you to (recursively)
crawl websites to generate word lists.`,
	Args: cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		paramDebug, err := cmd.LocalFlags().GetBool("debug")
		handleErr(err, false)
		paramDepth, err := cmd.LocalFlags().GetInt("depth")
		handleErr(err, false)
		paramMinLen, err := cmd.LocalFlags().GetInt("min-word-length")
		handleErr(err, false)
		paramMaxLen, err := cmd.LocalFlags().GetInt("max-word-length")
		handleErr(err, false)
		paramScope, err := cmd.LocalFlags().GetStringSlice("scope")
		handleErr(err, false)
		paramURLFilter, err := cmd.LocalFlags().GetString("url-filter")
		handleErr(err, false)
		paramOutput, err := cmd.LocalFlags().GetString("output")
		handleErr(err, false)
		paramNoFilter, err := cmd.LocalFlags().GetBool("no-filter")
		handleErr(err, false)
		paramJsonOutput, err := cmd.LocalFlags().GetBool("json")
		handleErr(err, false)
		sanitizedScope := []string{}
		for _, element := range paramScope {
			sanitizedScope = append(sanitizedScope, extractDomain(element))
		}
		for _, element := range args {
			sanitizedScope = append(sanitizedScope, extractDomain(element))
		}
		preparedTargets := []string{}
		for _, element := range args {
			preparedTargets = append(preparedTargets, toUri(element))
		}
		if contains(sanitizedScope, "*") {
			sanitizedScope = []string{}
		}
		var preparedFilters []*regexp.Regexp
		if len(paramURLFilter) > 0 {
			sanitizedScope = []string{} // so only filter affects
			preparedFilters = append(preparedFilters, regexp.MustCompile(paramURLFilter))
		}
		config := &skweezConf{
			debug:      paramDebug,
			depth:      paramDepth,
			minLen:     paramMinLen,
			maxLen:     paramMaxLen,
			scope:      sanitizedScope,
			urlFilter:  preparedFilters,
			output:     paramOutput,
			noFilter:   paramNoFilter,
			jsonOutput: paramJsonOutput,
			targets:    preparedTargets,
		}
		run(config)
	},
}

func Execute() {
	cobra.CheckErr(rootCmd.Execute())
}

func init() {
	rootCmd.Flags().IntP("depth", "d", 2, "Depth to spider. 0 = unlimited, 1 = Only provided site, 2... = specific depth")
	rootCmd.Flags().IntP("min-word-length", "m", 3, "Minimum word length")
	rootCmd.Flags().IntP("max-word-length", "n", 24, "Maximum word length")
	rootCmd.Flags().StringSlice("scope", []string{}, "Additional site scope, for example subdomains. If not set, only the provided site's domains are in scope. Using * disables scope checks (careful)")
	rootCmd.Flags().StringP("output", "o", "", "When set, write an output file")
	rootCmd.Flags().StringP("url-filter", "u", "", "Filter URL by regexp. .ie: \"(.*\\.)?domain\\.com.*\". Setting this will ignore scope")
	rootCmd.Flags().Bool("no-filter", false, "Do not filter out strings that don't match the regex to check if it looks like a valid word (starts and ends with alphanumeric letter, anything else in between). Also ignores --min-word-length and --max-word-length")
	rootCmd.Flags().Bool("json", false, "Write words + counts in a json file. Requires --output/-o")
	rootCmd.Flags().Bool("debug", false, "Enable Debug output")
}

func handleErr(err error, critical bool) {
	if err != nil {
		if critical {
			panic(err.Error())
		} else {
			fmt.Println(err.Error())
		}
	}
}

// https://play.golang.org/p/Qg_uv_inCek
// contains checks if a string is present in a slice
func contains(s []string, str string) bool {
	for _, v := range s {
		if v == str {
			return true
		}
	}

	return false
}

func initColly(config *skweezConf) *colly.Collector {
	c := colly.NewCollector(
		colly.MaxDepth(config.depth),
		colly.AllowedDomains(config.scope...),
		colly.URLFilters(config.urlFilter...),
	)
	c.AllowURLRevisit = false
	return c
}

func registerCallbacks(collector *colly.Collector, config *skweezConf, cache *map[string]int) {
	logger := log.New(os.Stderr, "", log.Ltime)

	collector.OnHTML("a[href]", func(e *colly.HTMLElement) {
		e.Request.Visit(e.Attr("href"))
	})

	collector.OnRequest(func(r *colly.Request) {
		if config.debug {
			logger.Println("Visiting", r.URL)
		}
	})

	collector.OnError(func(_ *colly.Response, err error) {
		if config.debug {
			logger.Println("Something went wrong:", err)
		}
	})

	collector.OnResponse(func(r *colly.Response) {
		if config.debug {
			logger.Println("Visited", r.Request.URL)
		}
	})

	collector.OnScraped(func(r *colly.Response) {
		// https://stackoverflow.com/questions/44441665/how-to-extract-only-text-from-html-in-golang
		logger.Println("Finished", r.Request.URL)

		extractWords(r.Body, config, cache)
	})
}

// cache should be a param, too. Allows for better testability
func extractWords(body []byte, config *skweezConf, cache *map[string]int) {
	domDoc := html.NewTokenizer(strings.NewReader(string(body)))
	previousStartTokenTest := domDoc.Token()
outer:
	for {
		tt := domDoc.Next()
		switch {
		case tt == html.ErrorToken:
			break outer
		case tt == html.StartTagToken:
			previousStartTokenTest = domDoc.Token()
		case tt == html.TextToken:
			if previousStartTokenTest.Data == "script" || previousStartTokenTest.Data == "style" {
				continue
			}
			TxtContent := strings.TrimSpace(html.UnescapeString(string(domDoc.Text())))
			if len(TxtContent) > 0 {
				unfilteredWords := strings.Split(TxtContent, " ")
				var filteredWords []string
				for _, word := range unfilteredWords {
					candidate := strings.Trim(word, stripTrailingSymbols)
					if config.noFilter {
						filteredWords = append(filteredWords, candidate)
					} else {
						if validWordRegex.MatchString(candidate) {
							if len(candidate) > config.minLen && len(candidate) < config.maxLen && allPrintable(word) {
								filteredWords = append(filteredWords, candidate)
							}
						}
					}
				}
				for _, word := range filteredWords {
					(*cache)[word] += 1
				}
			}
		}
	}
}

func run(config *skweezConf) {
	cache := make(map[string]int)
	c := initColly(config)
	registerCallbacks(c, config, &cache)

	for _, toVisit := range config.targets {
		c.Visit(toVisit)
	}
	outputResults(config, cache)

}

func outputResults(config *skweezConf, cache map[string]int) {
	if config.jsonOutput {
		jsonString, err := json.Marshal(cache)
		handleErr(err, false)
		if config.output != "" {
			mode := os.O_RDWR | os.O_CREATE
			filedescriptor, err := os.OpenFile(config.output, mode, 0644)
			handleErr(err, true)
			defer filedescriptor.Close()
			filedescriptor.Write(jsonString)
		} else {
			fmt.Println(string(jsonString[:]))
		}
	} else {
		if config.output != "" {
			mode := os.O_RDWR | os.O_CREATE
			filedescriptor, err := os.OpenFile(config.output, mode, 0644)
			handleErr(err, true)
			defer filedescriptor.Close()
			for word := range cache {
				filedescriptor.WriteString(fmt.Sprintf("%s\n", word))
			}
		} else {
			for word := range cache {
				fmt.Printf("%s\n", word)
			}
		}
	}
}

func extractDomain(uri string) string {
	if !strings.Contains(uri, "/") {
		return uri
	} else {
		// Strip https://, http:// and everything after and including the first slash of the remaining string
		// https://github.com/edermi/skweez -> github.com/edermi/skweez -> github.com
		noProto := strings.TrimPrefix(strings.TrimPrefix(uri, "http://"), "https://")
		return strings.Split(noProto, "/")[0]
	}
}

func toUri(domain string) string {
	if strings.HasPrefix(domain, "http://") || strings.HasPrefix(domain, "https://") {
		return domain
	} else {
		return "https://" + domain
	}
}

func allPrintable(word string) bool {
	for _, rune := range word {
		if !unicode.IsPrint(rune) {
			return false
		}
	}
	return true
}
