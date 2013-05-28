package geom

import (
	"errors"
	"goposm/element"
	"goposm/geom/geos"
)

type GeomError struct {
	message string
	level   int
}

func (e *GeomError) Error() string {
	return e.message
}

func (e *GeomError) Level() int {
	return e.level
}

func NewGeomError(message string, level int) *GeomError {
	return &GeomError{message, level}
}

var (
	ErrorOneNodeWay = NewGeomError("need at least two separate nodes for way", 0)
	ErrorNoRing     = NewGeomError("linestrings do not form ring", 0)
)

func PointWkb(g *geos.Geos, node element.Node) (*element.Geometry, error) {
	coordSeq, err := g.CreateCoordSeq(1, 2)
	if err != nil {
		return nil, err
	}
	// coordSeq inherited by LineString
	coordSeq.SetXY(g, 0, node.Long, node.Lat)
	geom, err := coordSeq.AsPoint(g)
	if err != nil {
		return nil, err
	}
	wkb := g.AsWkb(geom)
	if wkb == nil {
		g.Destroy(geom)
		return nil, errors.New("could not create wkb")
	}
	g.DestroyLater(geom)
	return &element.Geometry{
		Wkb:  wkb,
		Geom: geom,
	}, nil
}

func LineStringWkb(g *geos.Geos, nodes []element.Node) (*element.Geometry, error) {
	if len(nodes) < 2 {
		return nil, ErrorOneNodeWay
	}

	coordSeq, err := g.CreateCoordSeq(uint32(len(nodes)), 2)
	if err != nil {
		return nil, err
	}
	// coordSeq inherited by LineString
	for i, nd := range nodes {
		coordSeq.SetXY(g, uint32(i), nd.Long, nd.Lat)
	}
	geom, err := coordSeq.AsLineString(g)
	wkb := g.AsWkb(geom)
	if wkb == nil {
		g.Destroy(geom)
		return nil, errors.New("could not create wkb")
	}
	g.DestroyLater(geom)
	return &element.Geometry{
		Wkb:  wkb,
		Geom: geom,
	}, nil
}

func PolygonWkb(g *geos.Geos, nodes []element.Node) (*element.Geometry, error) {
	geom, err := Polygon(g, nodes)
	if err != nil {
		return nil, err
	}
	wkb := g.AsWkb(geom)
	if wkb == nil {
		return nil, errors.New("could not create wkb")
	}
	return &element.Geometry{
		Wkb:  wkb,
		Geom: geom,
	}, nil
}

func Polygon(g *geos.Geos, nodes []element.Node) (*geos.Geom, error) {
	coordSeq, err := g.CreateCoordSeq(uint32(len(nodes)), 2)
	if err != nil {
		return nil, err
	}
	// coordSeq inherited by LineString, no destroy
	for i, nd := range nodes {
		err := coordSeq.SetXY(g, uint32(i), nd.Long, nd.Lat)
		if err != nil {
			return nil, err
		}
	}
	ring, err := coordSeq.AsLinearRing(g)
	if err != nil {
		g.DestroyCoordSeq(coordSeq)
		return nil, err
	}
	// ring inherited by Polygon, no destroy

	geom := g.CreatePolygon(ring, nil)
	if geom == nil {
		g.Destroy(ring)
		return nil, errors.New("unable to create polygon")
	}
	g.DestroyLater(geom)
	return geom, nil
}
