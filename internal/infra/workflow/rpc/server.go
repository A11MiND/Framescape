package rpc

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"aigc-platform/internal/domain/workflow"
)

// Server exposes a workflow.Engine (the real one, living in cmd/scheduler)
// over HTTP so cmd/api can reach it without holding its own Engine instance.
type Server struct {
	eng workflow.Engine
}

func NewServer(eng workflow.Engine) *Server { return &Server{eng: eng} }

// Register mounts the internal engine routes onto r.
func (s *Server) Register(r gin.IRouter) {
	g := r.Group("/internal/engine")
	g.POST("/runs", s.submit)
	g.GET("/runs/:runID", s.get)
	g.POST("/runs/:runID/resume", s.resume)
	g.POST("/runs/:runID/cancel", s.cancel)
}

func fail(c *gin.Context, code int, err error) {
	c.JSON(code, ErrorResponse{Error: err.Error()})
}

func (s *Server) submit(c *gin.Context) {
	var req SubmitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	runID, err := s.eng.Submit(c.Request.Context(), &workflow.Definition{
		Name: req.DefinitionName,
		JSON: req.WorkflowJSON,
	}, req.Args)
	if err != nil {
		fail(c, http.StatusUnprocessableEntity, err)
		return
	}
	c.JSON(http.StatusOK, SubmitResponse{RunID: string(runID)})
}

func (s *Server) get(c *gin.Context) {
	run, err := s.eng.Get(c.Request.Context(), workflow.RunID(c.Param("runID")))
	if err != nil {
		fail(c, http.StatusNotFound, err)
		return
	}
	resp := GetResponse{ID: string(run.ID), Phase: run.Phase}
	for _, n := range run.Nodes {
		resp.Nodes = append(resp.Nodes, NodeStateWire{
			TaskRunID:    n.TaskRunID,
			Name:         n.Name,
			LoopIndex:    n.LoopIndex,
			ParentScope:  n.ParentScope,
			ExecutorType: n.ExecutorType,
			Phase:        n.Phase,
			ExecCode:     n.ExecCode,
			Outputs:      n.Outputs,
			ErrorMsg:     n.ErrorMsg,
		})
	}
	c.JSON(http.StatusOK, resp)
}

func (s *Server) resume(c *gin.Context) {
	var req ResumeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if err := s.eng.Resume(c.Request.Context(), workflow.RunID(c.Param("runID")), req.TaskName, req.Inputs); err != nil {
		fail(c, http.StatusUnprocessableEntity, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) cancel(c *gin.Context) {
	if err := s.eng.Cancel(c.Request.Context(), workflow.RunID(c.Param("runID"))); err != nil {
		fail(c, http.StatusUnprocessableEntity, err)
		return
	}
	c.Status(http.StatusNoContent)
}
