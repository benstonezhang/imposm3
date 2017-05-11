package mapping

import (
	"errors"
	"io/ioutil"

	"github.com/omniscale/imposm3/element"
	"github.com/omniscale/imposm3/mapping/config"

	"gopkg.in/yaml.v2"
)

type ElementFilter func(tags element.Tags, key Key, closed bool) bool

type orderedDestTable struct {
	DestTable
	order int
}

type TagTableMapping map[Key]map[Value][]orderedDestTable

func (tt TagTableMapping) addFromMapping(mapping config.KeyValues, table DestTable) {
	for key, vals := range mapping {
		for _, v := range vals {
			vals, ok := tt[Key(key)]
			tbl := orderedDestTable{DestTable: table, order: v.Order}
			if ok {
				vals[Value(v.Value)] = append(vals[Value(v.Value)], tbl)
			} else {
				tt[Key(key)] = make(map[Value][]orderedDestTable)
				tt[Key(key)][Value(v.Value)] = append(tt[Key(key)][Value(v.Value)], tbl)
			}
		}
	}
}

func (tt TagTableMapping) asTagMap() tagMap {
	result := make(tagMap)
	for k, vals := range tt {
		result[k] = make(map[Value]struct{})
		for v := range vals {
			result[k][v] = struct{}{}
		}
	}
	return result
}

type DestTable struct {
	Name       string
	SubMapping string
}

type TableType string

func (tt *TableType) UnmarshalJSON(data []byte) error {
	switch string(data) {
	case "":
		return errors.New("missing table type")
	case `"point"`:
		*tt = PointTable
	case `"linestring"`:
		*tt = LineStringTable
	case `"polygon"`:
		*tt = PolygonTable
	case `"geometry"`:
		*tt = GeometryTable
	case `"relation"`:
		*tt = RelationTable
	case `"relation_member"`:
		*tt = RelationMemberTable
	default:
		return errors.New("unknown type " + string(data))
	}
	return nil
}

const (
	PolygonTable        TableType = "polygon"
	LineStringTable     TableType = "linestring"
	PointTable          TableType = "point"
	GeometryTable       TableType = "geometry"
	RelationTable       TableType = "relation"
	RelationMemberTable TableType = "relation_member"
)

type Mapping struct {
	Conf config.Mapping
}

func NewMapping(filename string) (*Mapping, error) {
	f, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	mapping := Mapping{}
	err = yaml.Unmarshal(f, &mapping.Conf)
	if err != nil {
		return nil, err
	}

	err = mapping.prepare()
	if err != nil {
		return nil, err
	}
	return &mapping, nil
}

func (m *Mapping) prepare() error {
	for name, t := range m.Conf.Tables {
		t.Name = name
		if t.OldFields != nil {
			// todo deprecate 'fields'
			t.Columns = t.OldFields
		}
	}

	for name, t := range m.Conf.GeneralizedTables {
		t.Name = name
	}
	return nil
}

func (m *Mapping) mappings(tableType TableType, mappings TagTableMapping) {
	for name, t := range m.Conf.Tables {
		if TableType(t.Type) != GeometryTable && TableType(t.Type) != tableType {
			continue
		}
		mappings.addFromMapping(t.Mapping, DestTable{Name: name})

		for subMappingName, subMapping := range t.Mappings {
			mappings.addFromMapping(subMapping.Mapping, DestTable{Name: name, SubMapping: subMappingName})
		}

		switch tableType {
		case PointTable:
			mappings.addFromMapping(t.TypeMappings.Points, DestTable{Name: name})
		case LineStringTable:
			mappings.addFromMapping(t.TypeMappings.LineStrings, DestTable{Name: name})
		case PolygonTable:
			mappings.addFromMapping(t.TypeMappings.Polygons, DestTable{Name: name})
		}
	}
}

func (m *Mapping) tables(tableType TableType) map[string]*TableSpec {
	result := make(map[string]*TableSpec)
	for name, t := range m.Conf.Tables {
		if TableType(t.Type) == tableType || TableType(t.Type) == GeometryTable {
			result[name] = makeTableSpec(t)
		}
	}
	return result
}

func makeTableSpec(tbl *config.Table) *TableSpec {
	result := TableSpec{}

	for _, mappingColumn := range tbl.Columns {
		column := ColumnSpec{}
		column.Key = Key(mappingColumn.Key)

		columnType := MakeColumnType(mappingColumn)
		if columnType != nil {
			column.Type = *columnType
		} else {
			log.Warn("unhandled type: ", mappingColumn.Type)
		}
		result.columns = append(result.columns, column)
	}
	return &result
}

func MakeColumnType(c *config.Column) *ColumnType {
	if columnType, ok := AvailableColumnTypes[c.Type]; ok {
		if columnType.MakeFunc != nil {
			makeValue, err := columnType.MakeFunc(c.Name, columnType, *c)
			if err != nil {
				log.Print(err)
				return nil
			}
			columnType = ColumnType{columnType.Name, columnType.GoType, makeValue, nil, nil, columnType.FromMember}
		}
		columnType.FromMember = c.FromMember
		return &columnType
	}
	return nil
}

func (m *Mapping) extraTags(tableType TableType, tags map[Key]bool) {
	for _, t := range m.Conf.Tables {
		if TableType(t.Type) != tableType && TableType(t.Type) != GeometryTable {
			continue
		}

		for _, col := range t.Columns {
			if col.Key != "" {
				tags[Key(col.Key)] = true
			}
			for _, k := range col.Keys {
				tags[Key(k)] = true
			}
		}

		if t.Filters != nil && t.Filters.ExcludeTags != nil {
			for _, keyVal := range *t.Filters.ExcludeTags {
				tags[Key(keyVal[0])] = true
			}
		}

		if tableType == PolygonTable || tableType == RelationTable || tableType == RelationMemberTable {
			if t.RelationTypes != nil {
				tags["type"] = true
			}
		}
	}
	for _, k := range m.Conf.Tags.Include {
		tags[Key(k)] = true
	}

	// always include area tag for closed-way handling
	tags["area"] = true
}

type tableElementFilters map[string][]ElementFilter

func (m *Mapping) addTypedFilters(tableType TableType, filters tableElementFilters) {
	var areaTags map[Key]struct{}
	var linearTags map[Key]struct{}
	if m.Conf.Areas.AreaTags != nil {
		areaTags = make(map[Key]struct{})
		for _, tag := range m.Conf.Areas.AreaTags {
			areaTags[Key(tag)] = struct{}{}
		}
	}
	if m.Conf.Areas.LinearTags != nil {
		linearTags = make(map[Key]struct{})
		for _, tag := range m.Conf.Areas.LinearTags {
			linearTags[Key(tag)] = struct{}{}
		}
	}

	for name, t := range m.Conf.Tables {
		if TableType(t.Type) != GeometryTable && TableType(t.Type) != tableType {
			continue
		}
		if TableType(t.Type) == LineStringTable && areaTags != nil {
			f := func(tags element.Tags, key Key, closed bool) bool {
				if closed {
					if tags["area"] == "yes" {
						return false
					}
					if tags["area"] != "no" {
						if _, ok := areaTags[key]; ok {
							return false
						}
					}
				}
				return true
			}
			filters[name] = append(filters[name], f)
		}
		if TableType(t.Type) == PolygonTable && linearTags != nil {
			f := func(tags element.Tags, key Key, closed bool) bool {
				if closed && tags["area"] == "no" {
					return false
				}
				if tags["area"] != "yes" {
					if _, ok := linearTags[key]; ok {
						return false
					}
				}
				return true
			}
			filters[name] = append(filters[name], f)
		}
	}
}

func (m *Mapping) addRelationFilters(tableType TableType, filters tableElementFilters) {
	for name, t := range m.Conf.Tables {
		if t.RelationTypes != nil {
			relTypes := t.RelationTypes // copy loop var for closure
			f := func(tags element.Tags, key Key, closed bool) bool {
				if v, ok := tags["type"]; ok {
					for _, rtype := range relTypes {
						if v == rtype {
							return true
						}
					}
				}
				return false
			}
			filters[name] = append(filters[name], f)
		} else {
			if TableType(t.Type) == PolygonTable {
				// standard mulipolygon handling (boundary and land_area are for backwards compatibility)
				f := func(tags element.Tags, key Key, closed bool) bool {
					if v, ok := tags["type"]; ok {
						if v == "multipolygon" || v == "boundary" || v == "land_area" {
							return true
						}
					}
					return false
				}
				filters[name] = append(filters[name], f)
			}
		}
	}
}

func (m *Mapping) addFilters(filters tableElementFilters) {
	for name, t := range m.Conf.Tables {
		if t.Filters == nil {
			continue
		}
		if t.Filters.ExcludeTags != nil {
			for _, filterKeyVal := range *t.Filters.ExcludeTags {
				f := func(tags element.Tags, key Key, closed bool) bool {
					if v, ok := tags[filterKeyVal[0]]; ok {
						if filterKeyVal[1] == "__any__" || v == filterKeyVal[1] {
							return false
						}
					}
					return true
				}
				filters[name] = append(filters[name], f)
			}
		}
	}
}
