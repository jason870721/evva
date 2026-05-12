// Package notebook hosts the NotebookEdit tool.
package notebook

import "github.com/johnny1110/evva/internal/tools"

func init() { tools.Register(tools.NOTEBOOK_EDIT, Edit) }

// Names lists every tool name this package contributes.
func Names() []tools.ToolName { return []tools.ToolName{tools.NOTEBOOK_EDIT} }

var Edit tools.Tool = tools.NewStub(
	tools.NOTEBOOK_EDIT,
	"Replace, insert, or delete a cell in a Jupyter notebook (.ipynb). "+
		"The notebook_path must be absolute. Cells are 0-indexed. "+
		"Use edit_mode=insert to add a new cell after the index/cell_id; "+
		"edit_mode=delete to remove the cell at that index.",
	`{
		"type":"object",
		"additionalProperties":false,
		"required":["notebook_path","new_source"],
		"properties":{
			"notebook_path":{"type":"string","description":"The absolute path to the Jupyter notebook file (must be absolute, not relative)"},
			"new_source":{"type":"string","description":"The new source for the cell"},
			"cell_id":{"type":"string","description":"The ID of the cell to edit. When inserting, the new cell goes after this ID, or at the beginning if not specified."},
			"cell_type":{"type":"string","enum":["code","markdown"],"description":"Cell type. Defaults to current cell type. Required when edit_mode=insert."},
			"edit_mode":{"type":"string","enum":["replace","insert","delete"],"description":"The type of edit. Defaults to replace."}
		}
	}`,
)
