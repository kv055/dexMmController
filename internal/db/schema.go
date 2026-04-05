package db

import (
	"fmt"
	"gorm.io/gorm"
)

// Schema represents a database table definition
type Schema struct {
	TableName string
	Model     interface{} // GORM model struct
	Columns   []Column
}

// Column represents a table column definition
type Column struct {
	Name     string
	Type     string // e.g., "uint", "string", "float64", "time.Time"
	Tags     string // GORM tags
	Required bool
}

// SchemaManager handles database schema operations
type SchemaManager struct {
	db *gorm.DB
}

// NewSchemaManager creates a new schema manager
func NewSchemaManager(database *gorm.DB) *SchemaManager {
	return &SchemaManager{db: database}
}

// CreateSchema automatically migrates a model to create or update table
func (sm *SchemaManager) CreateSchema(model interface{}) error {
	if err := sm.db.AutoMigrate(model); err != nil {
		return fmt.Errorf("failed to create schema: %w", err)
	}
	return nil
}

// CreateSchemas automatically migrates multiple models
func (sm *SchemaManager) CreateSchemas(models ...interface{}) error {
	for _, model := range models {
		if err := sm.CreateSchema(model); err != nil {
			return err
		}
	}
	return nil
}

// DropTable drops a table by name
func (sm *SchemaManager) DropTable(tableName string) error {
	if err := sm.db.Migrator().DropTable(tableName); err != nil {
		return fmt.Errorf("failed to drop table %s: %w", tableName, err)
	}
	return nil
}

// TableExists checks if a table exists
func (sm *SchemaManager) TableExists(tableName string) bool {
	return sm.db.Migrator().HasTable(tableName)
}

// GetTableColumns returns all columns in a table
func (sm *SchemaManager) GetTableColumns(tableName string) ([]string, error) {
	columnTypes, err := sm.db.Migrator().ColumnTypes(tableName)
	if err != nil {
		return nil, fmt.Errorf("failed to get columns for table %s: %w", tableName, err)
	}
	columns := make([]string, len(columnTypes))
	for i, ct := range columnTypes {
		columns[i] = ct.Name()
	}
	return columns, nil
}

// GetTableSchema returns detailed information about a table
func (sm *SchemaManager) GetTableSchema(tableName string) (map[string]interface{}, error) {
	schema := make(map[string]interface{})

	// Check if table exists
	exists := sm.TableExists(tableName)
	schema["exists"] = exists

	if !exists {
		return schema, nil
	}

	// Get columns
	columns, err := sm.GetTableColumns(tableName)
	if err != nil {
		return nil, err
	}
	schema["columns"] = columns

	// Get column types
	columnTypes := make(map[string]string)
	ct, _ := sm.db.Migrator().ColumnTypes(tableName)
	for _, c := range ct {
		columnTypes[c.Name()] = c.DatabaseTypeName()
	}
	schema["column_types"] = columnTypes

	// Get indexes
	indexes, _ := sm.db.Migrator().GetIndexes(tableName)
	schema["indexes"] = indexes

	return schema, nil
}

// RegisterSchemas registers multiple table schemas
// This is useful for keeping all schema definitions in one place
func RegisterSchemas() map[string]interface{} {
	schemas := make(map[string]interface{})

	// Transaction schema
	schemas["Transaction"] = &Transaction{}

	// Add more schemas here as needed
	// schemas["User"] = &User{}
	// schemas["Strategy"] = &Strategy{}

	return schemas
}

// DescribeTable returns a human-readable description of a table schema
func (sm *SchemaManager) DescribeTable(tableName string) (string, error) {
	schema, err := sm.GetTableSchema(tableName)
	if err != nil {
		return "", err
	}

	exists := schema["exists"].(bool)
	if !exists {
		return fmt.Sprintf("Table '%s' does not exist", tableName), nil
	}

	columns := schema["columns"].([]string)
	columnTypes := schema["column_types"].(map[string]string)

	description := fmt.Sprintf("Table: %s\nColumns (%d):\n", tableName, len(columns))
	for _, col := range columns {
		colType := columnTypes[col]
		description += fmt.Sprintf("  - %s (%s)\n", col, colType)
	}

	return description, nil
}

// CreateTableFromSchema creates a table from a Schema definition
// This allows defining tables programmatically
func (sm *SchemaManager) CreateTableFromSchema(schema *Schema) error {
	if schema == nil {
		return fmt.Errorf("schema cannot be nil")
	}

	// Use GORM's AutoMigrate for the model
	if err := sm.db.AutoMigrate(schema.Model); err != nil {
		return fmt.Errorf("failed to create table from schema: %w", err)
	}

	return nil
}

// PrintAllSchemas prints all table schemas in the database
func (sm *SchemaManager) PrintAllSchemas() error {
	// Get all table names
	tables, _ := sm.db.Migrator().GetTables()

	fmt.Println("=== Database Schema ===")
	fmt.Println()

	for _, table := range tables {
		desc, err := sm.DescribeTable(table)
		if err != nil {
			return err
		}
		fmt.Println(desc)
		fmt.Println()
	}

	return nil
}

// ValidateSchema checks if a model's schema exists in the database
func (sm *SchemaManager) ValidateSchema(tableName string) error {
	if !sm.TableExists(tableName) {
		return fmt.Errorf("schema validation failed: table '%s' does not exist", tableName)
	}
	return nil
}
