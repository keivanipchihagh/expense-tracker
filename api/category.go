package api

import (
	"errors"
	"net/http"

	"github.com/keivanipchihagh/expense-tracker/pkg"
)

// ListCategories handles GET /api/v1/categories: the caller's categories,
// in order, with how many expenses use each.
func (s *Server) ListCategories(w http.ResponseWriter, r *http.Request) {
	list, err := s.handler.Categories(r.Context())
	if err != nil {
		writeHandlerError(w, r, err)
		return
	}
	writeJson(w, http.StatusOK, list)
}

// AddCategory handles POST /api/v1/categories with {"name"}.
func (s *Server) AddCategory(w http.ResponseWriter, r *http.Request) {
	input, err := decodeJsonObject(r)
	if err != nil {
		writeDecodeError(w, err)
		return
	}
	fieldErrors := map[string]string{}
	name := stringField(input, "name", fieldErrors)
	for key := range input {
		if key != "name" {
			fieldErrors[key] = "unknown field"
		}
	}
	if len(fieldErrors) > 0 {
		writeError(w, http.StatusUnprocessableEntity, codeValidationFailed, "validation failed", fieldErrors)
		return
	}
	c, err := s.handler.AddCategory(r.Context(), name)
	if err != nil {
		writeHandlerError(w, r, err)
		return
	}
	writeJson(w, http.StatusCreated, map[string]any{"name": c, "expenses": 0})
}

// RemoveCategory handles DELETE /api/v1/categories/{name}. It's refused
// (409) while expenses use the category.
func (s *Server) RemoveCategory(w http.ResponseWriter, r *http.Request) {
	if err := s.handler.RemoveCategory(r.Context(), r.PathValue("name")); err != nil {
		writeHandlerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// AddCategorySubmit handles POST /settings/categories.
func (s *Server) AddCategorySubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	name := r.PostForm.Get("name")
	c, err := s.handler.AddCategory(r.Context(), name)
	if appErr, ok := errors.AsType[*pkg.Error](err); ok && len(appErr.Fields) > 0 {
		s.renderAccount(w, r, http.StatusUnprocessableEntity, pageData{
			Errors:  map[string]string{"category": "Category " + appErr.Fields["name"] + "."},
			Form:    map[string]string{"category": name},
			Section: "categories",
		})
		return
	}
	if err != nil {
		s.renderUiError(w, r, err)
		return
	}
	s.renderAccount(w, r, http.StatusOK, pageData{Notice: "Added " + titleCase(string(c)) + ".", Section: "categories"})
}

// RemoveCategorySubmit handles POST /settings/categories/delete.
func (s *Server) RemoveCategorySubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	name := r.PostForm.Get("name")
	err := s.handler.RemoveCategory(r.Context(), name)
	if appErr, ok := errors.AsType[*pkg.Error](err); ok && appErr.Status != http.StatusInternalServerError && appErr.Status != http.StatusUnauthorized {
		s.renderAccount(w, r, appErr.Status, pageData{Errors: map[string]string{"category": appErr.Message}, Section: "categories"})
		return
	}
	if err != nil {
		s.renderUiError(w, r, err)
		return
	}
	s.renderAccount(w, r, http.StatusOK, pageData{Notice: "Removed " + titleCase(name) + ".", Section: "categories"})
}
