// Copyright 2016 Mender Software AS
//
//    Licensed under the Apache License, Version 2.0 (the "License");
//    you may not use this file except in compliance with the License.
//    You may obtain a copy of the License at
//
//        http://www.apache.org/licenses/LICENSE-2.0
//
//    Unless required by applicable law or agreed to in writing, software
//    distributed under the License is distributed on an "AS IS" BASIS,
//    WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//    See the License for the specific language governing permissions and
//    limitations under the License.

package controller

import (
	"context"
	"github.com/ant0ine/go-json-rest/rest"
	"github.com/asaskevich/govalidator"
	"github.com/mendersoftware/deployments/resources/deployments"
	"github.com/mendersoftware/deployments/utils/identity"
	"github.com/mendersoftware/go-lib-micro/requestid"
	"github.com/mendersoftware/go-lib-micro/requestlog"
	"github.com/mendersoftware/go-lib-micro/rest_utils"
	"github.com/pkg/errors"
	"net/http"
	"net/url"
)

// Errors
var (
	ErrIDNotUUIDv4                = errors.New("ID is not UUIDv4")
	ErrDeploymentID               = errors.New("Invalid deployment ID")
	ErrInternal                   = errors.New("Internal error")
	ErrDeploymentAlreadyFinished  = errors.New("Deployment already finished")
	ErrUnexpectedDeploymentStatus = errors.New("Unexpected deployment status")
)

type DeploymentsController struct {
	view  RESTView
	model DeploymentsModel
}

func NewDeploymentsController(model DeploymentsModel, view RESTView) *DeploymentsController {
	return &DeploymentsController{
		view:  view,
		model: model,
	}
}

func (d *DeploymentsController) PostDeployment(w rest.ResponseWriter, r *rest.Request) {
	l := requestlog.GetRequestLogger(r.Env)

	constructor, err := d.getDeploymentConstructorFromBody(r)
	if err != nil {
		d.view.RenderError(w, r, errors.Wrap(err, "Validating request body"), http.StatusBadRequest, l)
		return
	}

	reqId := requestid.GetReqId(r)
	ctx := context.WithValue(context.Background(), requestid.RequestIdHeader, reqId)
	id, err := d.model.CreateDeployment(ctx, constructor)
	if err != nil {
		d.view.RenderInternalError(w, r, err, l)
		return
	}

	d.view.RenderSuccessPost(w, r, id)
}

func (d *DeploymentsController) getDeploymentConstructorFromBody(r *rest.Request) (*deployments.DeploymentConstructor, error) {
	var constructor *deployments.DeploymentConstructor
	if err := r.DecodeJsonPayload(&constructor); err != nil {
		return nil, err
	}

	if err := constructor.Validate(); err != nil {
		return nil, err
	}

	return constructor, nil
}

func (d *DeploymentsController) GetDeployment(w rest.ResponseWriter, r *rest.Request) {
	l := requestlog.GetRequestLogger(r.Env)

	id := r.PathParam("id")

	if !govalidator.IsUUIDv4(id) {
		d.view.RenderError(w, r, ErrIDNotUUIDv4, http.StatusBadRequest, l)
		return
	}

	deployment, err := d.model.GetDeployment(id)
	if err != nil {
		d.view.RenderInternalError(w, r, err, l)
		return
	}

	if deployment == nil {
		d.view.RenderErrorNotFound(w, r, l)
		return
	}

	d.view.RenderSuccessGet(w, deployment)
}

func (d *DeploymentsController) GetDeploymentStats(w rest.ResponseWriter, r *rest.Request) {
	l := requestlog.GetRequestLogger(r.Env)

	id := r.PathParam("id")

	if !govalidator.IsUUIDv4(id) {
		d.view.RenderError(w, r, ErrIDNotUUIDv4, http.StatusBadRequest, l)
		return
	}

	stats, err := d.model.GetDeploymentStats(id)
	if err != nil {
		d.view.RenderInternalError(w, r, err, l)
		return
	}

	if stats == nil {
		d.view.RenderErrorNotFound(w, r, l)
		return
	}

	d.view.RenderSuccessGet(w, stats)
}

func (d *DeploymentsController) AbortDeployment(w rest.ResponseWriter, r *rest.Request) {
	l := requestlog.GetRequestLogger(r.Env)

	id := r.PathParam("id")

	if !govalidator.IsUUIDv4(id) {
		d.view.RenderError(w, r, ErrIDNotUUIDv4, http.StatusBadRequest, l)
		return
	}

	// receive request body
	var status struct {
		Status string
	}

	err := r.DecodeJsonPayload(&status)
	if err != nil {
		d.view.RenderError(w, r, err, http.StatusBadRequest, l)
		return
	}
	// "aborted" is the only supported status
	if status.Status != deployments.DeviceDeploymentStatusAborted {
		d.view.RenderError(w, r, ErrUnexpectedDeploymentStatus, http.StatusBadRequest, l)
	}

	// Check if deployment is finished
	isDeploymentFinished, err := d.model.IsDeploymentFinished(id)
	if err != nil {
		d.view.RenderInternalError(w, r, err, l)
		return
	}
	if isDeploymentFinished {
		d.view.RenderError(w, r, ErrDeploymentAlreadyFinished, http.StatusUnprocessableEntity, l)
		return
	}

	// Abort deployments for devices and update deployment stats
	if err := d.model.AbortDeployment(id); err != nil {
		d.view.RenderInternalError(w, r, err, l)
	}

	d.view.RenderEmptySuccessResponse(w)
}

const (
	GetDeploymentForDeviceQueryArtifact   = "artifact_name"
	GetDeploymentForDeviceQueryDeviceType = "device_type"
)

func (d *DeploymentsController) GetDeploymentForDevice(w rest.ResponseWriter, r *rest.Request) {

	l := requestlog.GetRequestLogger(r.Env)

	idata, err := identity.ExtractIdentityFromHeaders(r.Header)
	if err != nil {
		d.view.RenderError(w, r, err, http.StatusBadRequest, l)
		return
	}

	q := r.URL.Query()
	installed := deployments.InstalledDeviceDeployment{
		Artifact:   q.Get(GetDeploymentForDeviceQueryArtifact),
		DeviceType: q.Get(GetDeploymentForDeviceQueryDeviceType),
	}

	if err := installed.Validate(); err != nil {
		d.view.RenderError(w, r, err, http.StatusBadRequest, l)
		return
	}

	deployment, err := d.model.GetDeploymentForDeviceWithCurrent(idata.Subject, installed)
	if err != nil {
		d.view.RenderInternalError(w, r, err, l)
		return
	}

	if deployment == nil {
		d.view.RenderNoUpdateForDevice(w)
		return
	}

	d.view.RenderSuccessGet(w, deployment)
}

func (d *DeploymentsController) PutDeploymentStatusForDevice(w rest.ResponseWriter, r *rest.Request) {
	l := requestlog.GetRequestLogger(r.Env)

	did := r.PathParam("id")

	idata, err := identity.ExtractIdentityFromHeaders(r.Header)
	if err != nil {
		d.view.RenderError(w, r, err, http.StatusBadRequest, l)
		return
	}

	// receive request body
	var report statusReport

	err = r.DecodeJsonPayload(&report)
	if err != nil {
		d.view.RenderError(w, r, err, http.StatusBadRequest, l)
		return
	}

	status := report.Status
	if err := d.model.UpdateDeviceDeploymentStatus(did, idata.Subject, status); err != nil {
		if err == ErrDeploymentAborted || err == ErrDeviceDecommissioned {
			d.view.RenderError(w, r, err, http.StatusConflict, l)
		} else {
			d.view.RenderInternalError(w, r, err, l)
		}
		return
	}

	d.view.RenderEmptySuccessResponse(w)
}

func (d *DeploymentsController) GetDeviceStatusesForDeployment(w rest.ResponseWriter, r *rest.Request) {
	l := requestlog.GetRequestLogger(r.Env)

	did := r.PathParam("id")

	if !govalidator.IsUUIDv4(did) {
		d.view.RenderError(w, r, ErrIDNotUUIDv4, http.StatusBadRequest, l)
		return
	}

	statuses, err := d.model.GetDeviceStatusesForDeployment(did)
	if err != nil {
		switch err {
		case ErrModelDeploymentNotFound:
			d.view.RenderError(w, r, err, http.StatusNotFound, l)
			return
		default:
			d.view.RenderInternalError(w, r, ErrInternal, l)
			return
		}
	}

	d.view.RenderSuccessGet(w, statuses)
}

func ParseLookupQuery(vals url.Values) (deployments.Query, error) {
	query := deployments.Query{}

	search := vals.Get("search")
	if search != "" {
		query.SearchText = search
	}

	status := vals.Get("status")
	switch status {
	case "inprogress":
		query.Status = deployments.StatusQueryInProgress
	case "finished":
		query.Status = deployments.StatusQueryFinished
	case "pending":
		query.Status = deployments.StatusQueryPending
	case "aborted":
		query.Status = deployments.StatusQueryAborted
	case "":
		query.Status = deployments.StatusQueryAny
	default:
		return query, errors.Errorf("unknown status %s", status)

	}

	return query, nil
}

func (d *DeploymentsController) LookupDeployment(w rest.ResponseWriter, r *rest.Request) {
	l := requestlog.GetRequestLogger(r.Env)

	query, err := ParseLookupQuery(r.URL.Query())
	if err != nil {
		d.view.RenderError(w, r, err, http.StatusBadRequest, l)
		return
	}

	page, perPage, err := rest_utils.ParsePagination(r)
	if err != nil {
		d.view.RenderError(w, r, err, http.StatusBadRequest, l)
		return
	}
	query.Skip = int((page - 1) * perPage)
	query.Limit = int(perPage + 1)

	deps, err := d.model.LookupDeployment(query)
	if err != nil {
		d.view.RenderError(w, r, err, http.StatusBadRequest, l)
		return
	}

	len := len(deps)
	hasNext := false
	if uint64(len) > perPage {
		hasNext = true
		len = int(perPage)
	}

	links := rest_utils.MakePageLinkHdrs(r, page, perPage, hasNext)
	for _, l := range links {
		w.Header().Add("Link", l)
	}

	d.view.RenderSuccessGet(w, deps[:len])
}

func (d *DeploymentsController) PutDeploymentLogForDevice(w rest.ResponseWriter, r *rest.Request) {
	l := requestlog.GetRequestLogger(r.Env)

	did := r.PathParam("id")

	idata, err := identity.ExtractIdentityFromHeaders(r.Header)
	if err != nil {
		d.view.RenderError(w, r, err, http.StatusBadRequest, l)
		return
	}

	// reuse DeploymentLog, device and deployment IDs are ignored when
	// (un-)marshalling DeploymentLog to/from JSON
	var log deployments.DeploymentLog

	err = r.DecodeJsonPayload(&log)
	if err != nil {
		d.view.RenderError(w, r, err, http.StatusBadRequest, l)
		return
	}

	if err := d.model.SaveDeviceDeploymentLog(idata.Subject, did, log.Messages); err != nil {
		if err == ErrModelDeploymentNotFound {
			d.view.RenderError(w, r, err, http.StatusNotFound, l)
		} else {
			d.view.RenderInternalError(w, r, err, l)
		}
		return
	}

	d.view.RenderEmptySuccessResponse(w)
}

func (d *DeploymentsController) GetDeploymentLogForDevice(w rest.ResponseWriter, r *rest.Request) {
	l := requestlog.GetRequestLogger(r.Env)

	did := r.PathParam("id")
	devid := r.PathParam("devid")

	depl, err := d.model.GetDeviceDeploymentLog(devid, did)

	if err != nil {
		d.view.RenderInternalError(w, r, err, l)
		return
	}

	if depl == nil {
		d.view.RenderErrorNotFound(w, r, l)
		return
	}

	d.view.RenderDeploymentLog(w, *depl)
}

func (d *DeploymentsController) DecommissionDevice(w rest.ResponseWriter, r *rest.Request) {
	l := requestlog.GetRequestLogger(r.Env)

	id := r.PathParam("id")

	if !govalidator.IsUUIDv4(id) {
		d.view.RenderError(w, r, ErrIDNotUUIDv4, http.StatusBadRequest, l)
		return
	}

	// Decommission deployments for devices and update deployment stats
	if err := d.model.DecommissionDevice(id); err != nil {
		d.view.RenderInternalError(w, r, err, l)
	}

	d.view.RenderEmptySuccessResponse(w)
}
