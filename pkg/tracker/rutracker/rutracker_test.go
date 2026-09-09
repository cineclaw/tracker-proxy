package rutracker

import (
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
)

func TestParseRuTrackerHTML(t *testing.T) {
	htmlSnippet := `
	<table id="tor-tbl">
	  <tbody>
	    <tr class="tCenter hl-tr">
	      <td class="f-name"><a class="f" href="viewforum.php?f=7">Зарубежное кино</a></td>
	      <td class="t-title"><a data-topic_id="654321" class="tt-text" href="viewtopic.php?t=654321">Титаник / Titanic (1997) BDRip 1080p</a></td>
	      <td class="tor-size" data-ts_text="14500000000"><u>13.5 GB</u></td>
	      <td class="seedmed" data-ts_text="150"><b class="seedmed">150</b></td>
	      <td class="leechmed" data-ts_text="12">12</td>
	      <td data-ts_text="1708912345">2024-02-26</td>
	    </tr>
	  </tbody>
	</table>
	`

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlSnippet))
	if err != nil {
		t.Fatalf("failed to parse html: %v", err)
	}

	var count int
	doc.Find("table#tor-tbl tbody tr.hl-tr").Each(func(i int, s *goquery.Selection) {
		count++
		titleElem := s.Find("a.tt-text")
		if titleElem.Text() != "Титаник / Titanic (1997) BDRip 1080p" {
			t.Errorf("got title %s, want Титаник...", titleElem.Text())
		}
		id, _ := titleElem.Attr("data-topic_id")
		if id != "654321" {
			t.Errorf("got id %s, want 654321", id)
		}
		cat := s.Find("a.f").Text()
		if cat != "Зарубежное кино" {
			t.Errorf("got cat %s, want Зарубежное кино", cat)
		}
		sizeBytes, _ := s.Find("td.tor-size").Attr("data-ts_text")
		if sizeBytes != "14500000000" {
			t.Errorf("got size %s, want 14500000000", sizeBytes)
		}
		seeds, _ := s.Find("td.seedmed, b.seedmed").Attr("data-ts_text")
		if seeds != "150" {
			t.Errorf("got seeds %s, want 150", seeds)
		}
	})

	if count != 1 {
		t.Errorf("expected 1 row parsed, got %d", count)
	}
}
