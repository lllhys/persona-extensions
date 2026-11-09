package main

// The sample is original text written for this reader. Vocabulary mode underlines
// candidates inside it; translation mode translates the same blocks.

const articleOpen = "<article>\n<h1>A morning in the harbor</h1>\n"

const articleBody = "" +
	"<p>The lanterns along the quay still glowed when the merchant opened his stall. A resilient breeze carried the smell of tar and rope across the lane.</p>\n" +
	"<p>By noon the tide had shifted. Erosion had narrowed the old footpath, so travelers followed the brighter lane beside the granite dock. The forecast on the coast stayed calm.</p>\n" +
	"<p>The same resilient merchant looked over the harvest barrels and the quiet meadow beyond the orchard.</p>\n"

const articleExtra = "<p>A second vessel brought cargo to the wharf after that remarkable harvest.</p>\n"

const articleClose = "</article>"

func articleSource(more bool) string {
	if !more {
		return articleOpen + articleBody + articleClose
	}
	return articleOpen + articleBody + articleExtra + articleClose
}
