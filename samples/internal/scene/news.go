package scene

import (
	"math"
	"slices"

	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"

	slug "github.com/dhannyell/go-slug"
	"github.com/dhannyell/go-slug/text"
)

// Orbit is a view that the user controls. Only a scene with an orbit has one.
type Orbit struct {
	Yaw, Pitch float64
	Zoom       float64 // the scale of the image, as a power of 2
	X, Y       float64 // the move of the image, in parts of its height
}

// Drag turns the view by a move of the pointer, in pixels. A larger zoom turns less,
// so the point under the pointer stays near it.
func (o *Orbit) Drag(dx, dy float64) {
	k := 0.005 / max(1, math.Pow(2, o.Zoom))
	o.Yaw -= dx * k
	o.Pitch = min(max(o.Pitch-dy*k, -1.4), 1.4)
}

// Wheel zooms by a number of wheel steps, and keeps the point at x, y in place, in a
// target of w by h pixels.
func (o *Orbit) Wheel(steps, x, y float64, w, h int) {
	const smallest, largest = -1, 7
	before := o.Zoom
	o.Zoom = min(max(o.Zoom+steps/4, smallest), largest)
	if o.Zoom <= 0 {
		o.X, o.Y = 0, 0
		return
	}
	ratio, height := math.Pow(2, o.Zoom-before), float64(h)
	px, py := x-float64(w)/2, y-height/2
	o.X = (px - (px-o.X*height)*ratio) / height
	o.Y = (py - (py-o.Y*height)*ratio) / height
}

// image returns the move and the scale of the image, around its center.
func (o *Orbit) image(w, h int) Projection {
	k := math.Pow(2, o.Zoom)
	cx, cy := float64(w)/2, float64(h)/2
	return Projection{k, 0, cx + o.X*float64(h) - k*cx, 0, k, cy + o.Y*float64(h) - k*cy, 0, 0, 1}
}

const (
	newsWidth, newsHeight = 620.0, 860.0
	newsMargin            = 20.0
	newsColumns           = 3
	newsGap               = 12.0
	newsColumn            = (newsWidth - 2*newsMargin - (newsColumns-1)*newsGap) / newsColumns
	newsBody, newsLead    = 10.0, 13.0
	newsFirstFold         = newsMargin + newsColumn + newsGap/2

	// newsLeaves is the number of sheets. The first is the back page, and the last
	// is the front page.
	newsLeaves = 7
	newsFacets = 3
)

// newsEnds has the distance of each facet from the spine. A sheet bends in the
// gaps between the columns, so a line of body text stays on one plane.
var newsEnds = [newsFacets + 1]float64{0, newsFirstFold, newsWidth - newsFirstFold, newsWidth}

var (
	newsprint = [4]float32{0.92, 0.90, 0.84, 1}
	newsSky   = [4]float32{0.68, 0.68, 0.66, 1}
	newsLand  = [4]float32{0.30, 0.30, 0.32, 1}
)

// newsFonts are the three kinds of text of a page.
type newsFonts struct{ body, head, title *text.Font }

// news is a folded newspaper. The sheets go out from the spine, and bend a little.
// Each facet is one plane. The body text is at most 10 pixels per em at the start. A
// distance field made for the large title loses strokes at that size, and it reads
// well from about 14, so the wheel shows both.
func (s *Scene) news() error {
	var f newsFonts
	var err error
	if f.body, err = text.Parse(goregular.TTF, sourceShaped); err != nil {
		return err
	}
	if f.head, err = text.Parse(gobold.TTF, sourceNewsHead); err != nil {
		return err
	}
	if f.title, err = text.Parse(amiri, sourceNewsTitle); err != nil {
		return err
	}

	var leaves [newsLeaves][newsFacets]Span
	for i := range leaves {
		// A small change of color separates the sheets at their edges.
		shade := float32(0.93 + 0.03*float64(i%2))
		cover := i == 0 || i == newsLeaves-1
		if cover {
			shade = 1
		}
		if leaves[i], err = s.newsPaper(shade, cover); err != nil {
			return err
		}
	}
	front, err := s.newsPage(f, true, 0)
	if err != nil {
		return err
	}
	back, err := s.newsPage(f, false, 5)
	if err != nil {
		return err
	}
	s.Versus = true
	s.Orbit = &Orbit{Yaw: 0.38, Pitch: 0.12}
	s.frame = func(_ float64, w, h int) []Span { return newsFrame(leaves, front, back, s.Orbit, w, h) }
	return nil
}

// newsPaper adds the facets of one sheet, in coordinates from the spine. A cover is
// darker near the spine, where less light arrives. The other sheets stop before the
// spine, under the covers: many edges on one pixel add their rounding errors.
func (s *Scene) newsPaper(shade float32, cover bool) ([newsFacets]Span, error) {
	var spans [newsFacets]Span
	light := [newsFacets]float32{0.97, 1, 0.97}
	for j := range spans {
		spans[j].First = len(s.Items)
		c := newsprint
		for k := range 3 {
			c[k] *= shade * light[j]
		}
		// A facet goes under the next one, or the background shows at the fold.
		w := float32(newsEnds[j+1] - newsEnds[j])
		if j < newsFacets-1 {
			w++
		}
		var x float32
		if j == 0 && !cover {
			x = 3
		}
		if err := s.rect(x, 0, w-x, newsHeight, c); err != nil {
			return spans, err
		}
		if j == 0 && cover {
			for k := range 6 {
				shadow := [4]float32{0, 0, 0, 0.14 - 0.022*float32(k)}
				if err := s.rect(float32(k)*7, 0, 7, newsHeight, shadow); err != nil {
					return spans, err
				}
			}
		}
		spans[j].Count = len(s.Items) - spans[j].First
	}
	return spans, nil
}

// newsPage lays out one printed page, and cuts it into the facets. The articles
// repeat from first until the columns are full.
func (s *Scene) newsPage(f newsFonts, front bool, first int) ([newsFacets]Span, error) {
	start := len(s.Items)
	if err := s.newsLayout(f, front, first); err != nil {
		return [newsFacets]Span{}, err
	}
	page := slices.Clone(s.Items[start:])
	s.Items = s.Items[:start]

	var spans [newsFacets]Span
	for j := range spans {
		spans[j].First = len(s.Items)
		s.Items = append(s.Items, newsCut(page, float32(newsEnds[j]), float32(newsEnds[j+1]))...)
		spans[j].Count = len(s.Items) - spans[j].First
	}
	return spans, nil
}

// newsCut returns the part of a page from lo to hi, with lo at 0. A rectangle is cut
// at the ends, and a glyph goes with its center.
func newsCut(page []Item, lo, hi float32) []Item {
	var cut []Item
	for _, it := range page {
		if it.Plain() {
			x0, x1 := max(it.Place[4], lo), min(it.Place[4]+it.Place[0], hi)
			if x1 <= x0 {
				continue
			}
			it.Place[0], it.Place[4] = x1-x0, x0-lo
			cut = append(cut, it)
			continue
		}
		b := it.Entry.Bounds
		c := it.Place.Apply(slug.Point{X: (b.Min.X + b.Max.X) / 2, Y: (b.Min.Y + b.Max.Y) / 2})
		if c.X >= lo && c.X < hi {
			it.Place[4] -= lo
			cut = append(cut, it)
		}
	}
	return cut
}

// newsLayout adds the items of one page, in page coordinates.
func (s *Scene) newsLayout(f newsFonts, front bool, first int) error {
	const left, right = float32(newsMargin), float32(newsWidth - newsMargin)
	width := right - left
	gray := [4]float32{0, 0, 0, 0.10}
	rule := func(y, h float32) error { return s.rect(left, y, width, h, ink) }
	centered := func(t *text.Font, str string, size, y float32) error {
		l := t.Shape(str, text.Options{})[0]
		return s.place(t, l, size, (newsWidth-l.Width*size)/2, y, ink)
	}
	end := func(t *text.Font, str string, size, y float32) error {
		l := t.Shape(str, text.Options{})[0]
		return s.place(t, l, size, right-l.Width*size, y, ink)
	}

	// The fold of the sheet across its height.
	if err := s.rect(0, newsHeight/2-1, newsWidth, 2, gray); err != nil {
		return err
	}

	lead, deck := "Curves stay curves at each scale", "The shader reads the outline in each pixel, so a page has no size of its own."
	var y float32
	if front {
		if err := s.line(f.body, "LATE CITY EDITION", text.Options{}, 9, left, 22, ink); err != nil {
			return err
		}
		if err := end(f.body, "TWO BYTES", 9, 22); err != nil {
			return err
		}
		if err := centered(f.title, "The Daily Outline", 66, 72); err != nil {
			return err
		}
		if err := rule(88, 2.2); err != nil {
			return err
		}
		if err := rule(92, 0.7); err != nil {
			return err
		}
		if err := centered(f.body, "Vol. 1, No. 7    Tuesday morning    All the glyphs that fit", 10, 105); err != nil {
			return err
		}
		if err := rule(111, 0.7); err != nil {
			return err
		}
		y = 148
	} else {
		lead, deck = "Atlas closes after a long night", "The last shelf is full, and the staff went home at dawn."
		if err := s.line(f.body, "The Daily Outline", text.Options{}, 10, left, 22, ink); err != nil {
			return err
		}
		if err := end(f.body, "Page 48", 10, 22); err != nil {
			return err
		}
		if err := rule(28, 0.7); err != nil {
			return err
		}
		y = 66
	}
	if err := centered(f.head, lead, 32, y); err != nil {
		return err
	}
	if err := centered(f.body, deck, 13, y+20); err != nil {
		return err
	}
	y += 30
	if err := rule(y, 0.7); err != nil {
		return err
	}
	y += 8

	// The picture takes two columns: the right ones on the front, the left ones on
	// the back. The columns under it start lower.
	const picture = 190
	pictureColumn := 1
	if !front {
		pictureColumn = 0
	}
	x0 := left + float32(pictureColumn)*(newsColumn+newsGap)
	if err := s.newsPicture(x0, y, 2*newsColumn+newsGap, picture); err != nil {
		return err
	}
	if err := s.line(f.body, "An outline, at dawn, seen from the north side of the page.", text.Options{}, 8, x0, y+picture+10, ink); err != nil {
		return err
	}
	tops := [newsColumns]float32{y, y, y}
	for c := range 2 {
		tops[pictureColumn+c] = y + picture + 18
	}

	bottom := float32(newsHeight - 30)
	for c := range newsColumns - 1 {
		x := left + float32(c+1)*(newsColumn+newsGap) - newsGap/2
		top := max(tops[c], tops[c+1])
		if err := s.rect(x-0.3, top, 0.6, bottom-top, ink); err != nil {
			return err
		}
	}
	if err := rule(bottom+6, 0.7); err != nil {
		return err
	}
	if err := centered(f.body, "Continued on the next page, at each scale", 8, bottom+17); err != nil {
		return err
	}

	column, cy := 0, tops[0]
	// flow adds lines to the columns, and returns false when the last column is full.
	flow := func(t *text.Font, str string, size, leading float32) (bool, error) {
		for _, l := range t.Shape(str, text.Options{Width: newsColumn / size}) {
			if cy+leading > bottom {
				column++
				if column == newsColumns {
					return false, nil
				}
				cy = tops[column]
			}
			cy += leading
			x := left + float32(column)*(newsColumn+newsGap)
			if err := s.place(t, l, size, x, cy, ink); err != nil {
				return false, err
			}
		}
		return true, nil
	}
	for i := first; ; i++ {
		a := newsArticles[i%len(newsArticles)]
		more, err := flow(f.head, a.head, newsLead, newsLead*1.2)
		if err != nil {
			return err
		}
		if more {
			if more, err = flow(f.body, a.body, newsBody, newsBody*1.2); err != nil {
				return err
			}
		}
		if !more {
			return nil
		}
		cy += newsBody
	}
}

// newsPicture adds a gray picture of a city, from rectangles.
func (s *Scene) newsPicture(x, y, w, h float32) error {
	if err := s.rect(x, y, w, h, newsSky); err != nil {
		return err
	}
	towers := []struct{ at, width, height float32 }{
		{0, 0.10, 0.34}, {0.09, 0.08, 0.52}, {0.16, 0.12, 0.28}, {0.27, 0.07, 0.68}, {0.33, 0.10, 0.40},
		{0.42, 0.09, 0.58}, {0.50, 0.13, 0.30}, {0.62, 0.07, 0.76}, {0.68, 0.10, 0.46}, {0.77, 0.08, 0.60}, {0.84, 0.16, 0.36},
	}
	for _, t := range towers {
		if err := s.rect(x+t.at*w, y+h*(1-t.height), t.width*w, h*t.height, newsLand); err != nil {
			return err
		}
	}
	return nil
}

// newsSheet returns the line of one sheet at the top of the page, from the spine to
// the free edge, and the direction of each facet. The sheets open from the spine, so
// their free edges separate, and the whole paper has a small arch.
func newsSheet(i int) (points [newsFacets + 1]vec3, dirs [newsFacets]vec3) {
	open := -0.05 + 0.13*float64(i)/(newsLeaves-1)
	opening := [newsFacets]float64{1, 0.7, 0.4}
	arch := [newsFacets]float64{0.05, 0, -0.05}
	points[0] = vec3{-newsWidth / 2, newsHeight / 2, 0}
	for j := range dirs {
		sin, cos := math.Sincos(open*opening[j] + arch[j])
		dirs[j] = vec3{cos, 0, -sin}
		points[j+1] = points[j].add(dirs[j].scale(newsEnds[j+1] - newsEnds[j]))
	}
	return points, dirs
}

// newsFrame returns the sheets in the order from back to front. All sheets start at
// the spine, so the sheet with the farthest center cannot hide a nearer one.
func newsFrame(leaves [newsLeaves][newsFacets]Span, front, back [newsFacets]Span, o *Orbit, w, h int) []Span {
	const fov, fill = math.Pi / 6, 0.95
	turn := func(p vec3) vec3 { return rotateX(rotateY(p, o.Yaw), o.Pitch) }

	// The distance keeps the near edge in the image at each angle. In a large image, it
	// also keeps one pixel per unit, so the first view has the body at its size.
	focal := float64(h) / 2 / math.Tan(fov/2)
	fit := max(newsWidth/2+newsHeight/2/math.Tan(fov/2), newsWidth*focal/float64(w)) / fill
	eye := vec3{0, 0, -max(fit, focal)}
	cam := lookAt(eye, vec3{}, fov, h)
	image := o.image(w, h)
	v := turn(vec3{0, -1, 0})

	type sheet struct {
		spans    []Span
		distance float64
	}
	sheets := make([]sheet, 0, newsLeaves)
	for i, paper := range leaves {
		points, dirs := newsSheet(i)
		var pts [newsFacets + 1]vec3
		for j, p := range points {
			pts[j] = turn(p)
		}
		var sh sheet
		var printed []Span
		for j := range newsFacets {
			u := turn(dirs[j])
			center := pts[j].add(pts[j+1]).scale(0.5).add(v.scale(newsHeight / 2))
			facing := u.cross(v).dot(eye.sub(center).unit())
			if math.Abs(facing) < edgeCos {
				continue
			}
			view := cam.plane(pts[j], u, v, w, h).Then(image)
			sh.spans = append(sh.spans, Span{paper[j].First, paper[j].Count, view})
			// A page is on the outer side of its sheet. On the back page, the page
			// starts at the free edge.
			switch {
			case i == newsLeaves-1 && facing > 0:
				printed = append(printed, Span{front[j].First, front[j].Count, view})
			case i == 0 && facing < 0:
				page := back[newsFacets-1-j]
				printed = append(printed, Span{page.First, page.Count, cam.plane(pts[j+1], u.scale(-1), v, w, h).Then(image)})
			}
		}
		// The text goes over all of the paper, because a large glyph can cross a fold.
		sh.spans = append(sh.spans, printed...)
		middle := pts[0].add(pts[newsFacets]).scale(0.5).add(v.scale(newsHeight / 2))
		d := middle.sub(eye)
		sh.distance = d.dot(d)
		sheets = append(sheets, sh)
	}
	slices.SortStableFunc(sheets, func(a, b sheet) int {
		switch {
		case a.distance > b.distance:
			return -1
		case a.distance < b.distance:
			return 1
		}
		return 0
	})
	var spans []Span
	for _, sh := range sheets {
		spans = append(spans, sh.spans...)
	}
	return spans
}

// newsArticles is the text of the newspaper.
var newsArticles = []struct{ head, body string }{
	{"Letters refuse to blur",
		"A group of letters met at the edge of the page on Monday and said that they will keep their shape at each size. The letters said that a pixel is only a place to measure, and that the outline is the real letter. Witnesses said that the small letters spoke as clearly as the large ones."},
	{"Atlas reports full shelves",
		"The city atlas closed its doors at noon. Each shelf holds one image of one glyph, at one size, and the staff said that there is no room for a larger copy. A reader who asked for a sharper letter received a longer wait, and a note that gave the price per texel."},
	{"Distance field loses a stroke",
		"A thin stroke went missing from the distance field on Tuesday. Officials said that the stroke was thinner than two texels, and that no sample could see it. The stroke returned when the camera moved closer. The search continues for the dot of a small letter i."},
	{"Bands speed the morning shift",
		"Shader workers now read only the curves that can touch a pixel. The new band system divides each glyph into strips, and each strip lists its curves. A foreman said that the shift ends sooner, because no worker must read the whole outline to learn about one small corner of it."},
	{"Weather: clear edges all day",
		"Forecasters expect clear edges over the whole page, with light antialiasing near each corner. A low angle may arrive by evening from the east, and it can stretch the letters along the horizon. Readers do not need mipmap levels. Take a magnifying glass if you plan to travel far."},
	{"Quadratic curves win the vote",
		"The council voted in favor of quadratic curves on Wednesday. A cubic curve can still enter the city, but it must divide into smaller quadratic curves at the gate. The mayor said that the rule saves time, because a line crosses a quadratic curve at no more than two points."},
	{"Camera walks into a letter",
		"A camera walked into the corner of a capital L on Thursday and reported no damage. The corner stayed sharp at a scale of 256. The camera said that the view was simple, with one straight edge on each side, and that there was no staircase of pixels in the way."},
	{"Market report: bytes hold steady",
		"The price of a font in memory held steady this week. A full set of outlines costs less than a small atlas, and the cost does not change with the size of the text. Traders warn that the time per pixel is the true price, and that a large page of text must pay it."},
	{"Local plane has a perspective",
		"A flat plane on the north side of the city now has a perspective. Each of its pixels has its own scale, and the far side is smaller than the near side. Neighbors said that the text on the plane stays readable, and that the plane still looks flat from the front."},
	{"Letters to the editor",
		"Dear editor: I read this column from a turning sheet in a sample program, and I can read each word. My friend read it from a texture, and the small words broke into dust. Please tell your readers to compare the two sides of the screen before they trust a claim."},
}
