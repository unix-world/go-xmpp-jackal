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
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackal-xmpp/runqueue"
	"github.com/jackal-xmpp/sonar"
	"github.com/jackal-xmpp/stravaganza"
	streamerror "github.com/jackal-xmpp/stravaganza/errors/stream"
	"github.com/jackal-xmpp/stravaganza/jid"
	"github.com/ortuman/jackal/auth"
	"github.com/ortuman/jackal/model"
	xmppparser "github.com/ortuman/jackal/parser"
	"github.com/ortuman/jackal/router"
	"github.com/ortuman/jackal/router/stream"
	"github.com/ortuman/jackal/transport"
	"github.com/ortuman/jackal/transport/compress"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
)

func init() {
	disconnectTimeout = time.Second
}

func TestInC2S_SendElement(t *testing.T) {
	// given
	sessMock := &sessionMock{}

	var mtx sync.RWMutex
	sendBuf := bytes.NewBuffer(nil)

	sessMock.SendFunc = func(ctx context.Context, element stravaganza.Element) error {
		mtx.Lock()
		defer mtx.Unlock()
		_ = element.ToXML(sendBuf, true)
		return nil
	}
	s := &inC2S{
		session: sessMock,
		rq:      runqueue.New("in_c2s:test", nil),
	}
	// when
	stanza := stravaganza.NewBuilder("auth").
		WithAttribute(stravaganza.Namespace, saslNamespace).
		Build()

	s.SendElement(stanza)

	time.Sleep(time.Millisecond * 250)

	// then
	mtx.Lock()
	defer mtx.Unlock()

	require.Equal(t, `<auth xmlns="urn:ietf:params:xml:ns:xmpp-sasl"/>`, sendBuf.String())
}

func TestInC2S_Disconnect(t *testing.T) {
	// given
	trMock := &transportMock{}
	trMock.CloseFunc = func() error { return nil }

	sessMock := &sessionMock{}

	var mtx sync.RWMutex

	sendBuf := bytes.NewBuffer(nil)
	sessMock.SendFunc = func(ctx context.Context, element stravaganza.Element) error {
		mtx.Lock()
		defer mtx.Unlock()

		_ = element.ToXML(sendBuf, true)
		return nil
	}
	sessMock.CloseFunc = func(ctx context.Context) error { return nil }

	rmMock := &resourceManagerMock{}
	rmMock.DelResourceFunc = func(ctx context.Context, username string, resource string) error {
		return nil
	}

	routerMock := &routerMock{}
	c2sRouterMock := &c2sRouterMock{}

	c2sRouterMock.UnregisterFunc = func(stm stream.C2S) error { return nil }
	routerMock.C2SFunc = func() router.C2SRouter {
		return c2sRouterMock
	}
	s := &inC2S{
		state:   uint32(inBounded),
		session: sessMock,
		tr:      trMock,
		router:  routerMock,
		resMng:  rmMock,
		rq:      runqueue.New("in_c2s:test", nil),
		sn:      sonar.New(),
	}
	// when
	s.Disconnect(streamerror.E(streamerror.SystemShutdown))

	time.Sleep(disconnectTimeout + time.Second) // wait for disconnect

	// then
	mtx.Lock()
	defer mtx.Unlock()

	require.Equal(t, `<stream:error><system-shutdown xmlns="urn:ietf:params:xml:ns:xmpp-stanzas"/></stream:error>`, sendBuf.String())
	require.Len(t, sessMock.CloseCalls(), 1)
	require.Len(t, trMock.CloseCalls(), 1)
	require.Len(t, c2sRouterMock.UnregisterCalls(), 1)
	require.Len(t, rmMock.DelResourceCalls(), 1)
}

func TestInC2S_HandleSessionElement(t *testing.T) {
	jd0, _ := jid.New("ortuman", "jackal.im", "yard", true)
	jd1, _ := jid.New("ortuman", "jackal.im", "hall", true)
	jd2, _ := jid.New("ortuman", "jackal.im", "balcony", true)

	var tests = []struct {
		name string

		// input
		state         inC2SState
		sessionResFn  func() (stravaganza.Element, error)
		authProcessFn func(_ context.Context, _ stravaganza.Element) (stravaganza.Element, *auth.SASLError)
		routeError    error
		hubResources  []model.Resource
		flags         uint8

		// expectations
		expectedOutput        string
		expectRouted          bool
		expectResourceUpdated bool
		expectedState         inC2SState
	}{
		{
			name:  "Connecting/Unsecured",
			state: inConnecting,
			sessionResFn: func() (stravaganza.Element, error) {
				return stravaganza.NewBuilder("stream:stream").
					WithAttribute(stravaganza.Namespace, "jabber:client").
					WithAttribute(stravaganza.StreamNamespace, "http://etherx.jabber.org/streams").
					WithAttribute(stravaganza.To, "localhost").
					WithAttribute(stravaganza.Version, "1.0").
					Build(), nil
			},
			expectedOutput: `<?xml version="1.0"?><stream:stream xmlns="jabber:client" xmlns:stream="http://etherx.jabber.org/streams" id="c2s1" from="localhost" version="1.0"><stream:features xmlns:stream="http://etherx.jabber.org/streams" version="1.0"><starttls xmlns="urn:ietf:params:xml:ns:xmpp-tls"><required/></starttls></stream:features>`,
			expectedState:  inConnected,
		},
		{
			name:  "Connecting/Secured",
			state: inConnecting,
			flags: fSecured,
			sessionResFn: func() (stravaganza.Element, error) {
				return stravaganza.NewBuilder("stream:stream").
					WithAttribute(stravaganza.Namespace, "jabber:client").
					WithAttribute(stravaganza.StreamNamespace, "http://etherx.jabber.org/streams").
					WithAttribute(stravaganza.To, "localhost").
					WithAttribute(stravaganza.Version, "1.0").
					Build(), nil
			},
			expectedOutput: `<?xml version="1.0"?><stream:stream xmlns="jabber:client" xmlns:stream="http://etherx.jabber.org/streams" id="c2s1" from="localhost" version="1.0"><stream:features xmlns:stream="http://etherx.jabber.org/streams" version="1.0"><mechanisms xmlns="urn:ietf:params:xml:ns:xmpp-sasl"><mechanism>PLAIN</mechanism></mechanisms></stream:features>`,
			expectedState:  inConnected,
		},
		{
			name:  "Connecting/SecuredAndAuthenticated",
			state: inConnecting,
			flags: fSecured | fAuthenticated,
			sessionResFn: func() (stravaganza.Element, error) {
				return stravaganza.NewBuilder("stream:stream").
					WithAttribute(stravaganza.Namespace, "jabber:client").
					WithAttribute(stravaganza.StreamNamespace, "http://etherx.jabber.org/streams").
					WithAttribute(stravaganza.To, "localhost").
					WithAttribute(stravaganza.Version, "1.0").
					Build(), nil
			},
			expectedOutput: `<?xml version="1.0"?><stream:stream xmlns="jabber:client" xmlns:stream="http://etherx.jabber.org/streams" id="c2s1" from="localhost" version="1.0"><stream:features xmlns:stream="http://etherx.jabber.org/streams" version="1.0"><compression xmlns="http://jabber.org/features/compress"><method>zlib</method></compression><bind xmlns="urn:ietf:params:xml:ns:xmpp-bind"><required/></bind><session xmlns="urn:ietf:params:xml:ns:xmpp-session"/></stream:features>`,
			expectedState:  inAuthenticated,
		},
		{
			name:  "Connected/StartTLS",
			state: inConnected,
			sessionResFn: func() (stravaganza.Element, error) {
				return stravaganza.NewBuilder("starttls").
					WithAttribute(stravaganza.Namespace, tlsNamespace).
					Build(), nil
			},
			expectedOutput: `<proceed xmlns="urn:ietf:params:xml:ns:xmpp-tls"/>`,
			expectedState:  inConnecting,
		},
		{
			name:  "Connected/Authenticate",
			state: inConnected,
			flags: fSecured,
			sessionResFn: func() (stravaganza.Element, error) {
				return stravaganza.NewBuilder("auth").
					WithAttribute(stravaganza.Namespace, saslNamespace).
					WithAttribute("mechanism", "PLAIN").
					WithText("AG9ydHVtYW4AY29uMmNvam9uZXM=").
					Build(), nil
			},
			authProcessFn: func(_ context.Context, _ stravaganza.Element) (stravaganza.Element, *auth.SASLError) {
				return stravaganza.NewBuilder("success").
					WithAttribute(stravaganza.Namespace, saslNamespace).
					Build(), nil
			},
			expectedOutput: `<success xmlns="urn:ietf:params:xml:ns:xmpp-sasl"/>`,
			expectedState:  inConnecting,
		},
		{
			name:  "Connected/UnknownAuthMechanism",
			state: inConnected,
			flags: fSecured,
			sessionResFn: func() (stravaganza.Element, error) {
				return stravaganza.NewBuilder("auth").
					WithAttribute(stravaganza.Namespace, saslNamespace).
					WithAttribute("mechanism", "FOO-AUTH-MECHANISM").
					Build(), nil
			},
			expectedOutput: `<failure xmlns="urn:ietf:params:xml:ns:xmpp-sasl"><invalid-mechanism/></failure>`,
			expectedState:  inConnected,
		},
		{
			name:  "Connected/NotAuthorized",
			state: inConnected,
			flags: fSecured,
			sessionResFn: func() (stravaganza.Element, error) {
				return stravaganza.NewBuilder("iq").
					WithAttribute(stravaganza.Namespace, "jabber:client").
					WithAttribute(stravaganza.ID, "c2s20").
					WithAttribute(stravaganza.Type, "get").
					WithChild(
						stravaganza.NewBuilder("ping").
							WithAttribute(stravaganza.Namespace, "urn:xmpp:ping").
							Build(),
					).
					Build(), nil
			},
			expectedOutput: `<stream:error><not-authorized xmlns="urn:ietf:params:xml:ns:xmpp-stanzas"/></stream:error></stream:stream>`,
			expectedState:  inDisconnected,
		},
		{
			name:  "Connected/ServiceUnavailable",
			state: inConnected,
			flags: fSecured,
			sessionResFn: func() (stravaganza.Element, error) {
				return stravaganza.NewBuilder("iq").
					WithAttribute(stravaganza.Namespace, "jabber:client").
					WithAttribute(stravaganza.ID, "c2s20").
					WithAttribute(stravaganza.Type, "get").
					WithChild(
						stravaganza.NewBuilder("query").
							WithAttribute(stravaganza.Namespace, "jabber:iq:auth").
							Build(),
					).
					Build(), nil
			},
			expectedOutput: `<iq xmlns="jabber:client" id="c2s20" type="error"><query xmlns="jabber:iq:auth"/><error code="503" type="cancel"><service-unavailable xmlns="urn:ietf:params:xml:ns:xmpp-stanzas"/></error></iq>`,
			expectedState:  inConnected,
		},
		{
			name:  "Connected/UnsupportedStanzaType",
			state: inConnected,
			flags: fSecured,
			sessionResFn: func() (stravaganza.Element, error) {
				return stravaganza.NewBuilder("foo").
					WithAttribute(stravaganza.Namespace, "jabber:client").
					WithAttribute(stravaganza.ID, "c2s20").
					WithAttribute(stravaganza.Type, "get").
					WithChild(
						stravaganza.NewBuilder("ping").
							WithAttribute(stravaganza.Namespace, "urn:xmpp:ping").
							Build(),
					).
					Build(), nil
			},
			expectedOutput: `<stream:error><unsupported-stanza-type xmlns="urn:ietf:params:xml:ns:xmpp-stanzas"/></stream:error></stream:stream>`,
			expectedState:  inDisconnected,
		},
		{
			name:  "Authenticating/Success",
			state: inAuthenticating,
			flags: fSecured,
			sessionResFn: func() (stravaganza.Element, error) {
				return stravaganza.NewBuilder("auth").
					WithAttribute(stravaganza.Namespace, saslNamespace).
					WithAttribute("mechanism", "PLAIN").
					WithText("AG9ydHVtYW4AY29uMmNvam9uZXM=").
					Build(), nil
			},
			authProcessFn: func(_ context.Context, _ stravaganza.Element) (stravaganza.Element, *auth.SASLError) {
				return stravaganza.NewBuilder("success").
					WithAttribute(stravaganza.Namespace, saslNamespace).
					Build(), nil
			},
			expectedOutput: `<success xmlns="urn:ietf:params:xml:ns:xmpp-sasl"/>`,
			expectedState:  inConnecting,
		},
		{
			name:  "Authenticating/Fail",
			state: inAuthenticating,
			flags: fSecured | fCompressed,
			sessionResFn: func() (stravaganza.Element, error) {
				return stravaganza.NewBuilder("auth").
					WithAttribute(stravaganza.Namespace, saslNamespace).
					WithAttribute("mechanism", "PLAIN").
					WithText("AG9ydHVtYW4AY29uMmNvam9uZXM=").
					Build(), nil
			},
			authProcessFn: func(_ context.Context, _ stravaganza.Element) (stravaganza.Element, *auth.SASLError) {
				return nil, &auth.SASLError{Reason: auth.IncorrectEncoding}
			},
			expectedOutput: `<failure xmlns="urn:ietf:params:xml:ns:xmpp-sasl"><incorrect-encoding/></failure>`,
			expectedState:  inConnected,
		},
		{
			name:  "Authenticated/BindSuccess",
			state: inAuthenticated,
			flags: fSecured | fCompressed | fAuthenticated,
			sessionResFn: func() (stravaganza.Element, error) {
				iq, _ := stravaganza.NewIQBuilder().
					WithAttribute(stravaganza.From, "ortuman@localhost").
					WithAttribute(stravaganza.To, "ortuman@localhost").
					WithAttribute(stravaganza.Type, stravaganza.SetType).
					WithAttribute(stravaganza.ID, "bind_2").
					WithChild(
						stravaganza.NewBuilder("bind").
							WithAttribute(stravaganza.Namespace, bindNamespace).
							WithChild(
								stravaganza.NewBuilder("resource").WithText("yard").Build(),
							).
							Build(),
					).
					BuildIQ(false)
				return iq, nil
			},
			expectedOutput:        `<iq id="bind_2" type="result" from="ortuman@localhost" to="ortuman@localhost"><bind xmlns="urn:ietf:params:xml:ns:xmpp-bind"><jid>ortuman@localhost/yard</jid></bind></iq>`,
			expectedState:         inBounded,
			expectResourceUpdated: true,
		},
		{
			name:  "Authenticated/BindConflict",
			state: inAuthenticated,
			flags: fSecured | fCompressed | fAuthenticated,
			sessionResFn: func() (stravaganza.Element, error) {
				iq, _ := stravaganza.NewIQBuilder().
					WithAttribute(stravaganza.From, "ortuman@localhost").
					WithAttribute(stravaganza.To, "ortuman@localhost").
					WithAttribute(stravaganza.Type, stravaganza.SetType).
					WithAttribute(stravaganza.ID, "bind_2").
					WithChild(
						stravaganza.NewBuilder("bind").
							WithAttribute(stravaganza.Namespace, bindNamespace).
							WithChild(
								stravaganza.NewBuilder("resource").WithText("yard").Build(),
							).
							Build(),
					).
					BuildIQ(false)
				return iq, nil
			},
			hubResources: []model.Resource{
				{JID: jd0, InstanceID: "inst-2"},
			},
			expectedOutput: `<iq from="ortuman@localhost" to="ortuman@localhost" type="error" id="bind_2"><bind xmlns="urn:ietf:params:xml:ns:xmpp-bind"><resource>yard</resource></bind><error code="409" type="cancel"><conflict xmlns="urn:ietf:params:xml:ns:xmpp-stanzas"/></error></iq>`,
			expectedState:  inAuthenticated,
		},
		{
			name:  "Authenticated/BindMaxSessions",
			state: inAuthenticated,
			flags: fSecured | fCompressed | fAuthenticated,
			sessionResFn: func() (stravaganza.Element, error) {
				iq, _ := stravaganza.NewIQBuilder().
					WithAttribute(stravaganza.From, "ortuman@localhost").
					WithAttribute(stravaganza.To, "ortuman@localhost").
					WithAttribute(stravaganza.Type, stravaganza.SetType).
					WithAttribute(stravaganza.ID, "bind_2").
					WithChild(
						stravaganza.NewBuilder("bind").
							WithAttribute(stravaganza.Namespace, bindNamespace).
							WithChild(
								stravaganza.NewBuilder("resource").WithText("yard").Build(),
							).
							Build(),
					).
					BuildIQ(false)
				return iq, nil
			},
			hubResources: []model.Resource{ // default max allowed sessions (3)
				{JID: jd1, InstanceID: "inst-2"},
				{JID: jd2, InstanceID: "inst-2"},
				{JID: jd2, InstanceID: "inst-3"},
			},
			expectedOutput: `<stream:error><policy-violation xmlns="urn:ietf:params:xml:ns:xmpp-stanzas"/><reached-max-session-count xmlns="urn:xmpp:errors"/></stream:error></stream:stream>`,
			expectedState:  inDisconnected,
		},
		{
			name:  "Authenticated/CompressSuccess",
			state: inAuthenticated,
			flags: fSecured | fAuthenticated,
			sessionResFn: func() (stravaganza.Element, error) {
				return stravaganza.NewBuilder("compress").
					WithAttribute(stravaganza.Namespace, compressNamespace).
					WithChild(
						stravaganza.NewBuilder("method").
							WithText("zlib").
							Build(),
					).
					Build(), nil
			},
			expectedOutput: `<compressed xmlns="http://jabber.org/protocol/compress"/>`,
			expectedState:  inConnecting,
		},
		{
			name:  "Authenticated/CompressMalformed",
			state: inAuthenticated,
			flags: fSecured | fAuthenticated,
			sessionResFn: func() (stravaganza.Element, error) {
				return stravaganza.NewBuilder("compress").
					WithAttribute(stravaganza.Namespace, compressNamespace).
					Build(), nil
			},
			expectedOutput: `<failure xmlns="http://jabber.org/protocol/compress"><setup-failed/></failure>`,
			expectedState:  inAuthenticated,
		},
		{
			name:  "Authenticated/CompressUnsupportedMethod",
			state: inAuthenticated,
			flags: fSecured | fAuthenticated,
			sessionResFn: func() (stravaganza.Element, error) {
				return stravaganza.NewBuilder("compress").
					WithAttribute(stravaganza.Namespace, compressNamespace).
					WithChild(
						stravaganza.NewBuilder("method").
							WithText("lzw").
							Build(),
					).
					Build(), nil
			},
			expectedOutput: `<failure xmlns="http://jabber.org/protocol/compress"><unsupported-method/></failure>`,
			expectedState:  inAuthenticated,
		},
		{
			name:  "Bounded/InitSession",
			state: inBounded,
			flags: fSecured | fCompressed | fAuthenticated,
			sessionResFn: func() (stravaganza.Element, error) {
				iq, _ := stravaganza.NewIQBuilder().
					WithAttribute(stravaganza.From, "ortuman@localhost").
					WithAttribute(stravaganza.To, "ortuman@localhost").
					WithAttribute(stravaganza.Type, stravaganza.SetType).
					WithAttribute(stravaganza.ID, "session_2").
					WithChild(
						stravaganza.NewBuilder("session").
							WithAttribute(stravaganza.Namespace, sessionNamespace).
							Build(),
					).
					BuildIQ(false)
				return iq, nil
			},
			expectedOutput: `<iq id="session_2" type="result" from="ortuman@localhost" to="ortuman@localhost"/>`,
			expectedState:  inBounded,
		},
		{
			name:  "Bounded/InitSessionNotAllowed",
			state: inBounded,
			flags: fSecured | fCompressed | fAuthenticated | fSessionStarted,
			sessionResFn: func() (stravaganza.Element, error) {
				iq, _ := stravaganza.NewIQBuilder().
					WithAttribute(stravaganza.From, "ortuman@localhost").
					WithAttribute(stravaganza.To, "ortuman@localhost").
					WithAttribute(stravaganza.Type, stravaganza.SetType).
					WithAttribute(stravaganza.ID, "session_2").
					WithChild(
						stravaganza.NewBuilder("session").
							WithAttribute(stravaganza.Namespace, sessionNamespace).
							Build(),
					).
					BuildIQ(false)
				return iq, nil
			},
			expectedOutput: `<iq from="ortuman@localhost" to="ortuman@localhost" type="error" id="session_2"><session xmlns="urn:ietf:params:xml:ns:xmpp-session"/><error code="405" type="cancel"><not-allowed xmlns="urn:ietf:params:xml:ns:xmpp-stanzas"/></error></iq>`,
			expectedState:  inBounded,
		},
		{
			name:  "Bounded/RouteIQSuccess",
			state: inBounded,
			flags: fSecured | fCompressed | fAuthenticated | fSessionStarted,
			sessionResFn: func() (stravaganza.Element, error) {
				iq, _ := stravaganza.NewIQBuilder().
					WithAttribute(stravaganza.From, "ortuman@localhost/yard").
					WithAttribute(stravaganza.To, "noelia@localhost/hall").
					WithAttribute(stravaganza.Type, stravaganza.SetType).
					WithAttribute(stravaganza.ID, "iq_1").
					WithChild(
						stravaganza.NewBuilder("ping").
							WithAttribute(stravaganza.Namespace, "urn:xmpp:ping").
							Build(),
					).
					BuildIQ(false)
				return iq, nil
			},
			expectedState: inBounded,
			expectRouted:  true,
		},
		{
			name:  "Bounded/RouteIQResourceNotFound",
			state: inBounded,
			flags: fSecured | fCompressed | fAuthenticated | fSessionStarted,
			sessionResFn: func() (stravaganza.Element, error) {
				iq, _ := stravaganza.NewIQBuilder().
					WithAttribute(stravaganza.From, "ortuman@localhost/yard").
					WithAttribute(stravaganza.To, "noelia@localhost/hall").
					WithAttribute(stravaganza.Type, stravaganza.SetType).
					WithAttribute(stravaganza.ID, "iq_1").
					WithChild(
						stravaganza.NewBuilder("ping").
							WithAttribute(stravaganza.Namespace, "urn:xmpp:ping").
							Build(),
					).
					BuildIQ(false)
				return iq, nil
			},
			routeError:     router.ErrResourceNotFound,
			expectedOutput: `<iq from="noelia@localhost/hall" to="ortuman@localhost/yard" type="error" id="iq_1"><ping xmlns="urn:xmpp:ping"/><error code="503" type="cancel"><service-unavailable xmlns="urn:ietf:params:xml:ns:xmpp-stanzas"/></error></iq>`,
			expectedState:  inBounded,
		},
		{
			name:  "Bounded/RouteIQBlockedSender",
			state: inBounded,
			flags: fSecured | fCompressed | fAuthenticated | fSessionStarted,
			sessionResFn: func() (stravaganza.Element, error) {
				iq, _ := stravaganza.NewIQBuilder().
					WithAttribute(stravaganza.From, "ortuman@localhost/yard").
					WithAttribute(stravaganza.To, "noelia@localhost/hall").
					WithAttribute(stravaganza.Type, stravaganza.SetType).
					WithAttribute(stravaganza.ID, "iq_1").
					WithChild(
						stravaganza.NewBuilder("ping").
							WithAttribute(stravaganza.Namespace, "urn:xmpp:ping").
							Build(),
					).
					BuildIQ(false)
				return iq, nil
			},
			routeError:     router.ErrBlockedSender,
			expectedOutput: `<iq from="noelia@localhost/hall" to="ortuman@localhost/yard" type="error" id="iq_1"><ping xmlns="urn:xmpp:ping"/><error code="503" type="cancel"><service-unavailable xmlns="urn:ietf:params:xml:ns:xmpp-stanzas"/></error></iq>`,
			expectedState:  inBounded,
		},
		{
			name:  "Bounded/RouteIQFailedRemoteConnect",
			state: inBounded,
			flags: fSecured | fCompressed | fAuthenticated | fSessionStarted,
			sessionResFn: func() (stravaganza.Element, error) {
				iq, _ := stravaganza.NewIQBuilder().
					WithAttribute(stravaganza.From, "ortuman@localhost/yard").
					WithAttribute(stravaganza.To, "noelia@localhost/hall").
					WithAttribute(stravaganza.Type, stravaganza.SetType).
					WithAttribute(stravaganza.ID, "iq_1").
					WithChild(
						stravaganza.NewBuilder("ping").
							WithAttribute(stravaganza.Namespace, "urn:xmpp:ping").
							Build(),
					).
					BuildIQ(false)
				return iq, nil
			},
			routeError:     router.ErrRemoteServerNotFound,
			expectedOutput: `<iq from="noelia@localhost/hall" to="ortuman@localhost/yard" type="error" id="iq_1"><ping xmlns="urn:xmpp:ping"/><error code="404" type="cancel"><remote-server-not-found xmlns="urn:ietf:params:xml:ns:xmpp-stanzas"/></error></iq>`,
			expectedState:  inBounded,
		},
		{
			name:  "Bounded/RoutePresenceSuccess",
			state: inBounded,
			flags: fSecured | fCompressed | fAuthenticated | fSessionStarted,
			sessionResFn: func() (stravaganza.Element, error) {
				pr, _ := stravaganza.NewPresenceBuilder().
					WithAttribute(stravaganza.From, "ortuman@localhost/yard").
					WithAttribute(stravaganza.To, "noelia@localhost/hall").
					WithAttribute(stravaganza.Type, stravaganza.AvailableType).
					WithAttribute(stravaganza.ID, "pr_1").
					BuildPresence(false)
				return pr, nil
			},
			expectedState: inBounded,
			expectRouted:  true,
		},
		{
			name:  "Bounded/RoutePresenceUpdateResource",
			state: inBounded,
			flags: fSecured | fCompressed | fAuthenticated | fSessionStarted,
			sessionResFn: func() (stravaganza.Element, error) {
				pr, _ := stravaganza.NewPresenceBuilder().
					WithAttribute(stravaganza.From, "ortuman@localhost/yard").
					WithAttribute(stravaganza.To, "ortuman@localhost").
					WithAttribute(stravaganza.Type, stravaganza.AvailableType).
					WithAttribute(stravaganza.ID, "pr_1").
					BuildPresence(false)
				return pr, nil
			},
			expectedState:         inBounded,
			expectResourceUpdated: true,
		},
		{
			name:  "Bounded/RouteMessageSuccess",
			state: inBounded,
			flags: fSecured | fCompressed | fAuthenticated | fSessionStarted,
			sessionResFn: func() (stravaganza.Element, error) {
				pr, _ := stravaganza.NewMessageBuilder().
					WithAttribute(stravaganza.From, "ortuman@localhost/yard").
					WithAttribute(stravaganza.To, "noelia@localhost/hall").
					WithAttribute(stravaganza.Type, stravaganza.AvailableType).
					WithAttribute(stravaganza.ID, "msg_1").
					WithChild(
						stravaganza.NewBuilder("body").
							WithText("I'll give thee a wind.").
							Build(),
					).
					BuildMessage(false)
				return pr, nil
			},
			expectedState: inBounded,
			expectRouted:  true,
		},
		{
			name:  "Bounded/RouteMessageBlockedSender",
			state: inBounded,
			flags: fSecured | fCompressed | fAuthenticated | fSessionStarted,
			sessionResFn: func() (stravaganza.Element, error) {
				pr, _ := stravaganza.NewMessageBuilder().
					WithAttribute(stravaganza.From, "ortuman@localhost/yard").
					WithAttribute(stravaganza.To, "noelia@localhost/hall").
					WithAttribute(stravaganza.Type, stravaganza.AvailableType).
					WithAttribute(stravaganza.ID, "msg_1").
					WithChild(
						stravaganza.NewBuilder("body").
							WithText("I'll give thee a wind.").
							Build(),
					).
					BuildMessage(false)
				return pr, nil
			},
			routeError:     router.ErrBlockedSender,
			expectedOutput: `<message from="noelia@localhost/hall" to="ortuman@localhost/yard" type="error" id="msg_1"><body>I&#39;ll give thee a wind.</body><error code="503" type="cancel"><service-unavailable xmlns="urn:ietf:params:xml:ns:xmpp-stanzas"/></error></message>`,
			expectedState:  inBounded,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// given
			trMock := &transportMock{}
			hMock := &hostsMock{}
			ssMock := &sessionMock{}
			routerMock := &routerMock{}
			c2sRouterMock := &c2sRouterMock{}
			compsMock := &componentsMock{}
			modsMock := &modulesMock{}
			resMngMock := &resourceManagerMock{}
			repMock := &repositoryMock{}
			authMock := &authenticatorMock{}

			// transport mock
			trMock.TypeFunc = func() transport.Type { return transport.Socket }
			trMock.StartTLSFunc = func(cfg *tls.Config, asClient bool) {}
			trMock.EnableCompressionFunc = func(_ compress.Level) {}
			trMock.SetReadRateLimiterFunc = func(rLim *rate.Limiter) error { return nil }
			trMock.CloseFunc = func() error { return nil }

			// hosts mock
			hMock.IsLocalHostFunc = func(host string) bool { return host == "localhost" }
			hMock.CertificatesFunc = func() []tls.Certificate { return nil }

			// router mocks
			c2sRouterMock.BindFunc = func(id stream.C2SID) error { return nil }
			c2sRouterMock.UnregisterFunc = func(stm stream.C2S) error { return nil }

			routerMock.C2SFunc = func() router.C2SRouter {
				return c2sRouterMock
			}
			var routed bool
			routerMock.RouteFunc = func(ctx context.Context, stanza stravaganza.Stanza) error {
				if tt.routeError != nil {
					return tt.routeError
				}
				routed = true
				return nil
			}

			// components mock
			compsMock.IsComponentHostFunc = func(cHost string) bool { return false }

			// modules mock
			modsMock.StreamFeaturesFunc = func(_ context.Context, _ string) []stravaganza.Element { return nil }
			modsMock.IsModuleIQFunc = func(iq *stravaganza.IQ) bool { return false }

			// authenticator mock
			authMock.MechanismFunc = func() string { return "PLAIN" }
			authMock.AuthenticatedFunc = func() bool { return true }
			authMock.ResetFunc = func() {}
			authMock.UsernameFunc = func() string { return "ortuman" }
			authMock.ProcessElementFunc = tt.authProcessFn

			// session mock
			outBuf := bytes.NewBuffer(nil)
			ssMock.OpenStreamFunc = func(ctx context.Context, featuresElem stravaganza.Element) error {
				stmElem := stravaganza.NewBuilder("stream:stream").
					WithAttribute(stravaganza.Namespace, "jabber:client").
					WithAttribute(stravaganza.StreamNamespace, "http://etherx.jabber.org/streams").
					WithAttribute(stravaganza.ID, "c2s1").
					WithAttribute(stravaganza.From, "localhost").
					WithAttribute(stravaganza.Version, "1.0").
					WithChild(featuresElem).
					Build()

				outBuf.WriteString(`<?xml version="1.0"?>`)
				return stmElem.ToXML(outBuf, false)
			}
			ssMock.CloseFunc = func(_ context.Context) error {
				_, err := io.Copy(outBuf, strings.NewReader("</stream:stream>"))
				return err
			}
			ssMock.SendFunc = func(_ context.Context, element stravaganza.Element) error {
				return element.ToXML(outBuf, true)
			}
			ssMock.SetFromJIDFunc = func(_ *jid.JID) {}
			ssMock.ResetFunc = func(_ transport.Transport) error { return nil }

			// resourcemanager mock
			var updatedRes bool
			resMngMock.PutResourceFunc = func(_ context.Context, _ *model.Resource) error {
				updatedRes = true
				return nil
			}
			resMngMock.GetResourcesFunc = func(_ context.Context, _ string) ([]model.Resource, error) {
				return tt.hubResources, nil
			}
			resMngMock.DelResourceFunc = func(ctx context.Context, username string, resource string) error {
				return nil
			}
			// repository mock
			repMock.FetchBlockListItemsFunc = func(ctx context.Context, username string) ([]model.BlockListItem, error) {
				return nil, nil
			}

			userJID, _ := jid.NewWithString("ortuman@localhost", true)
			stm := &inC2S{
				opts: Options{
					KeepAliveTimeout: time.Minute,
					RequestTimeout:   time.Minute,
					MaxStanzaSize:    8192,
					CompressionLevel: compress.DefaultCompression,
					ResourceConflict: Disallow,
				},
				state:          uint32(tt.state),
				flags:          inC2SFlags{flg: tt.flags},
				rq:             runqueue.New(tt.name, nil),
				jd:             userJID,
				tr:             trMock,
				hosts:          hMock,
				router:         routerMock,
				mods:           modsMock,
				comps:          compsMock,
				authenticators: []auth.Authenticator{authMock},
				activeAuth:     authMock,
				session:        ssMock,
				resMng:         resMngMock,
				blockListRep:   repMock,
				sn:             sonar.New(),
			}
			// when
			stm.handleSessionResult(tt.sessionResFn())

			// then
			require.Equal(t, tt.expectedOutput, outBuf.String())
			require.Equal(t, tt.expectedState, stm.getState())
			require.Equal(t, tt.expectRouted, routed)
			require.Equal(t, tt.expectResourceUpdated, updatedRes)
		})
	}
}

func TestInC2S_HandleSessionError(t *testing.T) {
	var tests = []struct {
		name           string
		state          inC2SState
		sErr           error
		expectedOutput string
		expectClosed   bool
	}{
		{
			name:           "ClosedByPeerError",
			state:          inBounded,
			sErr:           xmppparser.ErrStreamClosedByPeer,
			expectedOutput: `</stream:stream>`,
			expectClosed:   true,
		},
		{
			name:           "EOFError",
			state:          inBounded,
			sErr:           io.EOF,
			expectedOutput: ``,
			expectClosed:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// given
			ssMock := &sessionMock{}
			trMock := &transportMock{}
			routerMock := &routerMock{}
			c2sRouterMock := &c2sRouterMock{}
			resMngMock := &resourceManagerMock{}

			outBuf := bytes.NewBuffer(nil)
			ssMock.OpenStreamFunc = func(_ context.Context, _ stravaganza.Element) error {
				_, err := io.Copy(outBuf, strings.NewReader("<stream:stream>"))
				return err
			}
			ssMock.CloseFunc = func(_ context.Context) error {
				_, err := io.Copy(outBuf, strings.NewReader("</stream:stream>"))
				return err
			}
			ssMock.SendFunc = func(_ context.Context, element stravaganza.Element) error {
				return element.ToXML(outBuf, true)
			}
			var trClosed bool
			trMock.CloseFunc = func() error {
				trClosed = true
				return nil
			}

			routerMock.C2SFunc = func() router.C2SRouter {
				return c2sRouterMock
			}
			c2sRouterMock.UnregisterFunc = func(stm stream.C2S) error { return nil }

			resMngMock.DelResourceFunc = func(ctx context.Context, username string, resource string) error {
				return nil
			}

			stm := &inC2S{
				opts: Options{
					KeepAliveTimeout: time.Minute,
					RequestTimeout:   time.Minute,
					MaxStanzaSize:    8192,
				},
				state:   uint32(tt.state),
				rq:      runqueue.New(tt.name, nil),
				tr:      trMock,
				session: ssMock,
				router:  routerMock,
				resMng:  resMngMock,
				sn:      sonar.New(),
			}
			// when
			stm.handleSessionResult(nil, tt.sErr)

			// then
			require.Equal(t, tt.expectedOutput, outBuf.String())
			require.Equal(t, tt.expectClosed, trClosed)
		})
	}
}
