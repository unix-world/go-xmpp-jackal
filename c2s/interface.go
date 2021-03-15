// Copyright 2020 The jackal Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package c2s

import (
	"context"
	"crypto/tls"

	"github.com/jackal-xmpp/stravaganza"
	streamerror "github.com/jackal-xmpp/stravaganza/errors/stream"
	"github.com/jackal-xmpp/stravaganza/jid"
	"github.com/ortuman/jackal/auth"
	"github.com/ortuman/jackal/model"
	"github.com/ortuman/jackal/repository"
	"github.com/ortuman/jackal/router"
	"github.com/ortuman/jackal/router/stream"
	"github.com/ortuman/jackal/transport"
)

//go:generate moq -out transport.mock_test.go . c2sTransport:transportMock
type c2sTransport interface {
	transport.Transport
}

//go:generate moq -out authenticator.mock_test.go . c2sAuthenticator:authenticatorMock
type c2sAuthenticator interface {
	auth.Authenticator
}

//go:generate moq -out repository.mock_test.go . c2sRepository:repositoryMock
type c2sRepository interface {
	repository.Repository
}

//go:generate moq -out router.mock_test.go . globalRouter:routerMock
type globalRouter interface {
	router.Router
}

//go:generate moq -out c2s_router.mock_test.go . globalC2SRouter:c2sRouterMock
type globalC2SRouter interface {
	router.C2SRouter
}

//go:generate moq -out hosts.mock_test.go . hosts
type hosts interface {
	Certificates() []tls.Certificate
	IsLocalHost(host string) bool
}

//go:generate moq -out session.mock_test.go . session
type session interface {
	SetFromJID(ssJID *jid.JID)

	Send(ctx context.Context, element stravaganza.Element) error
	Receive() (stravaganza.Element, error)

	OpenStream(ctx context.Context, featuresElem stravaganza.Element) error
	Close(ctx context.Context) error

	Reset(tr transport.Transport) error
}

//go:generate moq -out localrouter.mock_test.go . localRouter
type localRouter interface {
	Route(stanza stravaganza.Stanza, username, resource string) error
	Disconnect(username, resource string, streamErr *streamerror.Error) error

	Register(stm stream.C2S) error
	Bind(id stream.C2SID) (stream.C2S, error)
	Unregister(stm stream.C2S) error

	Stream(username, resource string) stream.C2S

	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}

//go:generate moq -out clusterrouter.mock_test.go . clusterRouter
type clusterRouter interface {
	Route(ctx context.Context, stanza stravaganza.Stanza, username, resource, instanceID string) error
	Disconnect(ctx context.Context, username, resource string, streamErr *streamerror.Error, instanceID string) error

	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}

//go:generate moq -out components.mock_test.go . components
type components interface {
	IsComponentHost(cHost string) bool
	ProcessStanza(ctx context.Context, stanza stravaganza.Stanza) error
}

//go:generate moq -out modules.mock_test.go . modules
type modules interface {
	StreamFeatures(ctx context.Context, domain string) []stravaganza.Element

	IsModuleIQ(iq *stravaganza.IQ) bool
	ProcessIQ(ctx context.Context, iq *stravaganza.IQ) error

	IsEnabled(modName string) bool
}

//go:generate moq -out resourcemanager.mock_test.go . resourceManager
type resourceManager interface {
	PutResource(ctx context.Context, resource *model.Resource) error
	GetResources(ctx context.Context, username string) ([]model.Resource, error)
	DelResource(ctx context.Context, username, resource string) error
}
