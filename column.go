package main

import "fmt"
import "strconv"
import "database/sql"
import "github.com/joncrlsn/pgutil"

// ColumnSchema holds a channel streaming column data from one of the databases as well as
// a reference to the current row of data we're viewing.
//
// ColumnSchema implements the Schema interface defined in pgdiff.go
type ColumnSchema struct {
	channel chan map[string]string
	row     map[string]string
	done    bool
}

// NextRow reads from the channel and tells you if there are (probably) more or not
func (c *ColumnSchema) NextRow() bool {
	c.row = <-c.channel
	if len(c.row) == 0 {
		c.done = true
	}
	return !c.done
}

// Compare tells you, in one pass, whether or not the first row matches, is less than, or greater than the second row
func (c ColumnSchema) Compare(obj interface{}) int {
	c2, ok := obj.(*ColumnSchema)
	if !ok {
		fmt.Println("Error!!!, change needs a ColumnSchema instance", c2)
	}

	val := _compareString(c.row["table_name"], c2.row["table_name"])
	if val != 0 {
		// Table name differed so return that value
		return val
	}

	// Table name was the same so compare column name
	val = _compareString(c.row["column_name"], c2.row["column_name"])
	return val
}

// Add returns SQL to add the column
func (c ColumnSchema) Add() {
	if c.row["data_type"] == "character varying" {
		maxLength := c.row["character_maximum_length"]
		if maxLength == "null" {
			fmt.Println("-- WARNING: varchar column has no maximum length.  Setting to 1024")
			maxLength = "1024"
		}
		fmt.Printf("ALTER TABLE %s ADD COLUMN %s %s(%s)", c.row["table_name"], c.row["column_name"], c.row["data_type"], maxLength)
	} else {
		fmt.Printf("ALTER TABLE %s ADD COLUMN %s %s", c.row["table_name"], c.row["column_name"], c.row["data_type"])
	}

	if c.row["is_nullable"] == "NO" {
		fmt.Printf(" NOT NULL")
	}
	if c.row["column_default"] != "null" {
		fmt.Printf(" DEFAULT %s", c.row["column_default"])
	}
	fmt.Printf(";\n")
}

// Drop returns SQL to drop the column
func (c ColumnSchema) Drop() {
	// if dropping column
	fmt.Printf("ALTER TABLE %s DROP COLUMN %s;\n", c.row["table_name"], c.row["column_name"])
}

// Change handles the case where the table and column match, but the details do not
func (c ColumnSchema) Change(obj interface{}) {
	c2, ok := obj.(*ColumnSchema)
	if !ok {
		fmt.Println("Error!!!, ColumnSchema.Change(obj) needs a ColumnSchema instance", c2)
	}

	// Detect column type change (mostly varchar length, or number size increase)  (integer to/from bigint is OK)
	if c.row["data_type"] == c2.row["data_type"] {
		if c.row["data_type"] == "character varying" {
			if c.row["character_maximum_length"] != c2.row["character_maximum_length"] {
				max1 := c.row["character_maximum_length"]
				max2 := c2.row["character_maximum_length"]
				if max1 != "null" && max2 != "null" {
					cMax, err1 := strconv.Atoi(max1)
					check("converting string to int", err1)
					c2Max, err2 := strconv.Atoi(max2)
					check("converting string to int", err2)
					if cMax < c2Max {
						fmt.Println("-- WARNING: The next statement will shorten a character varying column.")
					}
				}
				maxLength := c.row["character_maximum_length"]
				if maxLength == "null" {
					fmt.Println("-- WARNING: varchar column has no maximum length.  Setting to 1024")
					maxLength = "1024"
				}
				fmt.Printf("ALTER TABLE %s ALTER COLUMN %s TYPE character varying(%s);\n", c.row["table_name"], c.row["column_name"], maxLength)
			}
		}
	}

	// TODO: Code and test a column change from integer to bigint
	if c.row["data_type"] != c2.row["data_type"] {
		fmt.Printf("-- WARNING: This type change may not work well: (%s to %s).\n", c2.row["data_type"], c.row["data_type"])
		fmt.Printf("ALTER TABLE %s ALTER COLUMN %s TYPE %s;\n", c.row["table_name"], c.row["column_name"], c.row["data_type"])
	}

	// Detect column default change (or added, dropped)
	if c.row["column_default"] == "null" {
		if c.row["column_default"] != "null" {
			fmt.Printf("ALTER TABLE %s ALTER COLUMN %s DROP DEFAULT;\n", c.row["table_name"], c.row["column_name"])
		}
	} else if c.row["column_default"] != c2.row["column_default"] {
		fmt.Printf("ALTER TABLE %s ALTER COLUMN %s SET DEFAULT %s;\n", c.row["table_name"], c.row["column_name"], c.row["column_default"])
	}

	// TODO Detect not-null and nullable change
	if c.row["is_nullable"] != c2.row["is_nullable"] {
		if c.row["is_nullable"] == "YES" {
			fmt.Printf("ALTER TABLE %s ALTER COLUMN %s DROP NOT NULL;\n", c.row["table_name"], c.row["column_name"])
		} else {
			fmt.Printf("ALTER TABLE %s ALTER COLUMN %s SET NOT NULL;\n", c.row["table_name"], c.row["column_name"])
		}
	}
}

/*
 * Compare the columns in the two databases
 */
func compareColumns(conn1 *sql.DB, conn2 *sql.DB) {
	sql := `
SELECT table_name
    , column_name
    , data_type
    , is_nullable
    , column_default
    , character_maximum_length
FROM information_schema.columns 
WHERE table_schema = 'public'
AND is_updatable = 'YES'
ORDER by table_name, column_name COLLATE "C" ASC;`

	rowChan1, _ := pgutil.QueryStrings(conn1, sql)
	rowChan2, _ := pgutil.QueryStrings(conn2, sql)

	// We have to explicitly type this as Schema for some reason
	var schema1 Schema = &ColumnSchema{channel: rowChan1}
	var schema2 Schema = &ColumnSchema{channel: rowChan2}

	// Compare the columns
	doDiff(schema1, schema2)
}
