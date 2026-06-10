package db

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

type canonicalSchemaHashPayload struct {
	Tables         []canonicalSchemaHashTable           `json:"tables"`
	Indexes        []canonicalIndexRequirement          `json:"indexes"`
	ColumnMetadata []canonicalColumnMetadataRequirement `json:"columnMetadata"`
	ForeignKeys    []canonicalForeignKeyRequirement     `json:"foreignKeys"`
	FTSObjects     []canonicalFTSRequirement            `json:"ftsObjects"`
	Triggers       []canonicalTriggerRequirement        `json:"triggers"`
}

type canonicalSchemaHashTable struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
}

func canonicalSchemaContractHash() string {
	payload := canonicalSchemaHashPayload{
		Tables:         canonicalSchemaHashTables(),
		Indexes:        sortedCanonicalIndexes(),
		ColumnMetadata: sortedCanonicalColumnMetadata(),
		ForeignKeys:    sortedCanonicalForeignKeys(),
		FTSObjects:     sortedCanonicalFTSObjects(),
		Triggers:       sortedCanonicalTriggers(),
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func canonicalSchemaHashTables() []canonicalSchemaHashTable {
	tables := make([]string, 0, len(canonicalMainSchema))
	for table := range canonicalMainSchema {
		tables = append(tables, table)
	}
	sort.Strings(tables)

	payload := make([]canonicalSchemaHashTable, 0, len(tables))
	for _, table := range tables {
		columns := append([]string(nil), canonicalMainSchema[table]...)
		payload = append(payload, canonicalSchemaHashTable{Name: table, Columns: columns})
	}
	return payload
}

func sortedCanonicalIndexes() []canonicalIndexRequirement {
	values := append([]canonicalIndexRequirement(nil), canonicalMainIndexes...)
	sort.Slice(values, func(i, j int) bool {
		if values[i].Table != values[j].Table {
			return values[i].Table < values[j].Table
		}
		return values[i].Name < values[j].Name
	})
	return values
}

func sortedCanonicalColumnMetadata() []canonicalColumnMetadataRequirement {
	values := append([]canonicalColumnMetadataRequirement(nil), canonicalColumnMetadata...)
	sort.Slice(values, func(i, j int) bool {
		if values[i].Table != values[j].Table {
			return values[i].Table < values[j].Table
		}
		return values[i].Column < values[j].Column
	})
	return values
}

func sortedCanonicalForeignKeys() []canonicalForeignKeyRequirement {
	values := append([]canonicalForeignKeyRequirement(nil), canonicalForeignKeys...)
	sort.Slice(values, func(i, j int) bool {
		left := values[i]
		right := values[j]
		if left.Table != right.Table {
			return left.Table < right.Table
		}
		if left.Column != right.Column {
			return left.Column < right.Column
		}
		if left.ReferencedTable != right.ReferencedTable {
			return left.ReferencedTable < right.ReferencedTable
		}
		return left.ReferencedColumn < right.ReferencedColumn
	})
	return values
}

func sortedCanonicalFTSObjects() []canonicalFTSRequirement {
	values := append([]canonicalFTSRequirement(nil), canonicalFTSObjects...)
	sort.Slice(values, func(i, j int) bool {
		return values[i].Table < values[j].Table
	})
	return values
}

func sortedCanonicalTriggers() []canonicalTriggerRequirement {
	values := append([]canonicalTriggerRequirement(nil), canonicalFTSTriggers...)
	sort.Slice(values, func(i, j int) bool {
		if values[i].Table != values[j].Table {
			return values[i].Table < values[j].Table
		}
		return values[i].Name < values[j].Name
	})
	return values
}
