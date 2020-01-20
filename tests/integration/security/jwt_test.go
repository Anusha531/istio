// Copyright 2019 Istio Authors
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

package security

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"istio.io/istio/pkg/test/echo/common/response"
	"istio.io/istio/pkg/test/echo/common/scheme"
	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/framework/components/echo"
	"istio.io/istio/pkg/test/framework/components/echo/echoboot"
	"istio.io/istio/pkg/test/framework/components/environment"
	"istio.io/istio/pkg/test/framework/components/ingress"
	"istio.io/istio/pkg/test/framework/components/namespace"
	"istio.io/istio/pkg/test/util/file"
	"istio.io/istio/pkg/test/util/retry"
	"istio.io/istio/pkg/test/util/tmpl"
	"istio.io/istio/tests/common/jwt"
	"istio.io/istio/tests/integration/security/util"
	"istio.io/istio/tests/integration/security/util/authn"
	"istio.io/istio/tests/integration/security/util/connection"
)

const (
	authHeaderKey = "Authorization"
)

func TestAuthnJwt(t *testing.T) {
	testIssuer1Token := jwt.TokenIssuer1
	testIssuer2Token := jwt.TokenIssuer2

	framework.NewTest(t).
		RequiresEnvironment(environment.Kube).
		Run(func(ctx framework.TestContext) {
			ns := namespace.NewOrFail(t, ctx, namespace.Config{
				Prefix: "authn-jwt",
				Inject: true,
			})

			// Apply the policy.
			namespaceTmpl := map[string]string{
				"Namespace": ns.Name(),
			}
			jwtPolicies := tmpl.EvaluateAllOrFail(t, namespaceTmpl,
				file.AsStringOrFail(t, "testdata/jwt/simple-jwt-policy.yaml.tmpl"),
				file.AsStringOrFail(t, "testdata/jwt/jwt-with-paths.yaml.tmpl"),
				file.AsStringOrFail(t, "testdata/jwt/two-issuers.yaml.tmpl"))
			g.ApplyConfigOrFail(t, ns, jwtPolicies...)
			defer g.DeleteConfigOrFail(t, ns, jwtPolicies...)

			var a, b, c, d, e echo.Instance
			echoboot.NewBuilderOrFail(ctx, ctx).
				With(&a, util.EchoConfig("a", ns, false, nil, g, p)).
				With(&b, util.EchoConfig("b", ns, false, nil, g, p)).
				With(&c, util.EchoConfig("c", ns, false, nil, g, p)).
				With(&d, util.EchoConfig("d", ns, false, nil, g, p)).
				With(&e, util.EchoConfig("e", ns, false, nil, g, p)).
				BuildOrFail(t)

			testCases := []authn.TestCase{
				{
					Name: "jwt-simple-valid-token",
					Request: connection.Checker{
						From: a,
						Options: echo.CallOptions{
							Target:   b,
							PortName: "http",
							Scheme:   scheme.HTTP,
							Headers: map[string][]string{
								authHeaderKey: {"Bearer " + testIssuer1Token},
							},
						},
					},
					ExpectResponseCode: response.StatusCodeOK,
				},
				{
					Name: "jwt-simple-expired-token",
					Request: connection.Checker{
						From: a,
						Options: echo.CallOptions{
							Target:   b,
							PortName: "http",
							Scheme:   scheme.HTTP,
							Headers: map[string][]string{
								authHeaderKey: {"Bearer " + jwt.TokenExpired},
							},
						},
					},
					ExpectResponseCode: response.StatusUnauthorized,
				},
				{
					Name: "jwt-simple-no-token",
					Request: connection.Checker{
						From: a,
						Options: echo.CallOptions{
							Target:   b,
							PortName: "http",
							Scheme:   scheme.HTTP,
						},
					},
					ExpectResponseCode: response.StatusUnauthorized,
				},
				{
					Name: "jwt-excluded-paths-no-token[/health_check]",
					Request: connection.Checker{
						From: a,
						Options: echo.CallOptions{
							Target:   c,
							Path:     "/health_check",
							PortName: "http",
							Scheme:   scheme.HTTP,
						},
					},
					ExpectResponseCode: response.StatusCodeOK,
				},
				{
					Name: "jwt-excluded-paths-no-token[/guest-us]",
					Request: connection.Checker{
						From: a,
						Options: echo.CallOptions{
							Target:   c,
							Path:     "/guest-us",
							PortName: "http",
							Scheme:   scheme.HTTP,
						},
					},
					ExpectResponseCode: response.StatusCodeOK,
				},
				{
					Name: "jwt-excluded-paths-no-token[/index.html]",
					Request: connection.Checker{
						From: a,
						Options: echo.CallOptions{
							Target:   c,
							Path:     "/index.html",
							PortName: "http",
							Scheme:   scheme.HTTP,
						},
					},
					ExpectResponseCode: response.StatusUnauthorized,
				},
				{
					Name: "jwt-excluded-paths-valid-token",
					Request: connection.Checker{
						From: a,
						Options: echo.CallOptions{
							Target:   c,
							Path:     "/index.html",
							PortName: "http",
							Scheme:   scheme.HTTP,
							Headers: map[string][]string{
								authHeaderKey: {"Bearer " + testIssuer1Token},
							},
						},
					},
					ExpectResponseCode: response.StatusCodeOK,
				},
				{
					Name: "jwt-included-paths-no-token[/index.html]",
					Request: connection.Checker{
						From: a,
						Options: echo.CallOptions{
							Target:   d,
							Path:     "/index.html",
							PortName: "http",
							Scheme:   scheme.HTTP,
						},
					},
					ExpectResponseCode: response.StatusCodeOK,
				},
				{
					Name: "jwt-included-paths-no-token[/something-confidential]",
					Request: connection.Checker{
						From: a,
						Options: echo.CallOptions{
							Target:   d,
							Path:     "/something-confidential",
							PortName: "http",
							Scheme:   scheme.HTTP,
						},
					},
					ExpectResponseCode: response.StatusUnauthorized,
				},
				{
					Name: "jwt-included-paths-valid-token",
					Request: connection.Checker{
						From: a,
						Options: echo.CallOptions{
							Target:   d,
							Path:     "/something-confidential",
							PortName: "http",
							Scheme:   scheme.HTTP,
							Headers: map[string][]string{
								authHeaderKey: {"Bearer " + testIssuer1Token},
							},
						},
					},
					ExpectResponseCode: response.StatusCodeOK,
				},
				{
					Name: "jwt-two-issuers-no-token",
					Request: connection.Checker{
						From: a,
						Options: echo.CallOptions{
							Target:   e,
							PortName: "http",
							Scheme:   scheme.HTTP,
						},
					},
					ExpectResponseCode: response.StatusUnauthorized,
				},
				{
					Name: "jwt-two-issuers-token2",
					Request: connection.Checker{
						From: a,
						Options: echo.CallOptions{
							Target:   e,
							PortName: "http",
							Scheme:   scheme.HTTP,
							Headers: map[string][]string{
								authHeaderKey: {"Bearer " + testIssuer2Token},
							},
						},
					},
					ExpectResponseCode: response.StatusCodeOK,
				},
				{
					Name: "jwt-two-issuers-token1",
					Request: connection.Checker{
						From: a,
						Options: echo.CallOptions{
							Target:   e,
							PortName: "http",
							Scheme:   scheme.HTTP,
							Headers: map[string][]string{
								authHeaderKey: {"Bearer " + testIssuer1Token},
							},
						},
					},
					ExpectResponseCode: response.StatusUnauthorized,
				},
				{
					Name: "jwt-two-issuers-invalid-token",
					Request: connection.Checker{
						From: a,
						Options: echo.CallOptions{
							Target:   e,
							PortName: "http",
							Path:     "/testing-istio-jwt",
							Scheme:   scheme.HTTP,
							Headers: map[string][]string{
								authHeaderKey: {"Bearer " + jwt.TokenInvalid},
							},
						},
					},
					ExpectResponseCode: response.StatusUnauthorized,
				},
				{
					Name: "jwt-two-issuers-token1[/testing-istio-jwt]",
					Request: connection.Checker{
						From: a,
						Options: echo.CallOptions{
							Target:   e,
							PortName: "http",
							Scheme:   scheme.HTTP,
							Path:     "/testing-istio-jwt",
							Headers: map[string][]string{
								authHeaderKey: {"Bearer " + testIssuer1Token},
							},
						},
					},
					ExpectResponseCode: response.StatusCodeOK,
				},
				{
					Name: "jwt-two-issuers-token2[/testing-istio-jwt]",
					Request: connection.Checker{
						From: a,
						Options: echo.CallOptions{
							Target:   e,
							PortName: "http",
							Scheme:   scheme.HTTP,
							Path:     "/testing-istio-jwt",
							Headers: map[string][]string{
								authHeaderKey: {"Bearer " + testIssuer2Token},
							},
						},
					},
					ExpectResponseCode: response.StatusUnauthorized,
				},
				{
					Name: "jwt-wrong-issuers",
					Request: connection.Checker{
						From: a,
						Options: echo.CallOptions{
							Target:   e,
							Path:     "/wrong_issuer",
							PortName: "http",
							Scheme:   scheme.HTTP,
							Headers: map[string][]string{
								authHeaderKey: {"Bearer " + testIssuer1Token},
							},
						},
					},
					ExpectResponseCode: response.StatusUnauthorized,
				},
			}

			for _, c := range testCases {
				t.Run(c.Name, func(t *testing.T) {
					retry.UntilSuccessOrFail(t, c.CheckAuthn,
						retry.Delay(250*time.Millisecond), retry.Timeout(30*time.Second))
				})
			}
		})
}

// TestRequestAuthentication tests beta authn policy for jwt.
func TestRequestAuthentication(t *testing.T) {
	payload1 := strings.Split(jwt.TokenIssuer1, ".")[1]
	payload2 := strings.Split(jwt.TokenIssuer2, ".")[1]
	framework.NewTest(t).
		RequiresEnvironment(environment.Kube).
		Run(func(ctx framework.TestContext) {
			ns := namespace.NewOrFail(t, ctx, namespace.Config{
				Prefix: "req-authn",
				Inject: true,
			})

			// Apply the policy.
			namespaceTmpl := map[string]string{
				"Namespace": ns.Name(),
			}
			jwtPolicies := tmpl.EvaluateAllOrFail(t, namespaceTmpl,
				file.AsStringOrFail(t, "testdata/requestauthn/b-authn-authz.yaml.tmpl"),
				file.AsStringOrFail(t, "testdata/requestauthn/c-authn.yaml.tmpl"),
				file.AsStringOrFail(t, "testdata/requestauthn/e-authn.yaml.tmpl"),
			)
			g.ApplyConfigOrFail(t, ns, jwtPolicies...)
			defer g.DeleteConfigOrFail(t, ns, jwtPolicies...)

			var a, b, c, d, e echo.Instance
			echoboot.NewBuilderOrFail(ctx, ctx).
				With(&a, util.EchoConfig("a", ns, false, nil, g, p)).
				With(&b, util.EchoConfig("b", ns, false, nil, g, p)).
				With(&c, util.EchoConfig("c", ns, false, nil, g, p)).
				With(&d, util.EchoConfig("d", ns, false, nil, g, p)).
				With(&e, util.EchoConfig("e", ns, false, nil, g, p)).
				BuildOrFail(t)

			testCases := []authn.TestCase{
				{
					Name: "valid-token-noauthz",
					Request: connection.Checker{
						From: a,
						Options: echo.CallOptions{
							Target:   c,
							PortName: "http",
							Scheme:   scheme.HTTP,
							Headers: map[string][]string{
								authHeaderKey: {"Bearer " + jwt.TokenIssuer1},
							},
						},
					},
					ExpectResponseCode: response.StatusCodeOK,
					ExpectHeaders: map[string]string{
						authHeaderKey:    "",
						"X-Test-Payload": payload1,
					},
				},
				{
					Name: "valid-token-2-noauthz",
					Request: connection.Checker{
						From: a,
						Options: echo.CallOptions{
							Target:   c,
							PortName: "http",
							Scheme:   scheme.HTTP,
							Headers: map[string][]string{
								authHeaderKey: {"Bearer " + jwt.TokenIssuer2},
							},
						},
					},
					ExpectResponseCode: response.StatusCodeOK,
					ExpectHeaders: map[string]string{
						authHeaderKey:    "",
						"X-Test-Payload": payload2,
					},
				},
				{
					Name: "expired-token-noauthz",
					Request: connection.Checker{
						From: a,
						Options: echo.CallOptions{
							Target:   c,
							PortName: "http",
							Scheme:   scheme.HTTP,
							Headers: map[string][]string{
								authHeaderKey: {"Bearer " + jwt.TokenExpired},
							},
						},
					},
					ExpectResponseCode: response.StatusUnauthorized,
				},
				{
					Name: "no-token-noauthz",
					Request: connection.Checker{
						From: a,
						Options: echo.CallOptions{
							Target:   c,
							PortName: "http",
							Scheme:   scheme.HTTP,
						},
					},
					ExpectResponseCode: response.StatusCodeOK,
				},
				// Following app b is configured with authorization, only request with valid JWT succeed.
				{
					Name: "valid-token",
					Request: connection.Checker{
						From: a,
						Options: echo.CallOptions{
							Target:   b,
							PortName: "http",
							Scheme:   scheme.HTTP,
							Headers: map[string][]string{
								authHeaderKey: {"Bearer " + jwt.TokenIssuer1},
							},
						},
					},
					ExpectResponseCode: response.StatusCodeOK,
					ExpectHeaders: map[string]string{
						authHeaderKey: "",
					},
				},
				{
					Name: "expired-token",
					Request: connection.Checker{
						From: a,
						Options: echo.CallOptions{
							Target:   b,
							PortName: "http",
							Scheme:   scheme.HTTP,
							Headers: map[string][]string{
								authHeaderKey: {"Bearer " + jwt.TokenExpired},
							},
						},
					},
					ExpectResponseCode: response.StatusUnauthorized,
				},
				{
					Name: "no-token",
					Request: connection.Checker{
						From: a,
						Options: echo.CallOptions{
							Target:   b,
							PortName: "http",
							Scheme:   scheme.HTTP,
						},
					},
					ExpectResponseCode: response.StatusCodeForbidden,
				},
				{
					Name: "no-authn-authz",
					Request: connection.Checker{
						From: a,
						Options: echo.CallOptions{
							Target:   d,
							PortName: "http",
							Scheme:   scheme.HTTP,
						},
					},
					ExpectResponseCode: response.StatusCodeOK,
				},
				{
					Name: "valid-token-forward",
					Request: connection.Checker{
						From: a,
						Options: echo.CallOptions{
							Target:   e,
							PortName: "http",
							Scheme:   scheme.HTTP,
							Headers: map[string][]string{
								authHeaderKey: {"Bearer " + jwt.TokenIssuer1},
							},
						},
					},
					ExpectResponseCode: response.StatusCodeOK,
					ExpectHeaders: map[string]string{
						authHeaderKey:    "Bearer " + jwt.TokenIssuer1,
						"X-Test-Payload": payload1,
					},
				},
			}
			for _, c := range testCases {
				t.Run(c.Name, func(t *testing.T) {
					retry.UntilSuccessOrFail(t, c.CheckAuthn,
						retry.Delay(250*time.Millisecond), retry.Timeout(30*time.Second))
				})
			}
		})
}

// TestIngressRequestAuthentication tests beta authn policy for jwt on ingress.
// The policy is also set at global namespace, with authorization on ingressgateway.
func TestIngressRequestAuthentication(t *testing.T) {
	framework.NewTest(t).
		RequiresEnvironment(environment.Kube).
		Run(func(ctx framework.TestContext) {
			var ingr ingress.Instance
			var err error
			if ingr, err = ingress.New(ctx, ingress.Config{
				Istio: ist,
			}); err != nil {
				t.Fatal(err)
			}

			ns := namespace.NewOrFail(t, ctx, namespace.Config{
				Prefix: "req-authn-ingress",
				Inject: true,
			})

			// Apply the policy.
			namespaceTmpl := map[string]string{
				"Namespace":     ns.Name(),
				"RootNamespace": rootNamespace,
			}

			applyPolicy := func(filename string, ns namespace.Instance) []string {
				policy := tmpl.EvaluateAllOrFail(t, namespaceTmpl, file.AsStringOrFail(t, filename))
				g.ApplyConfigOrFail(t, ns, policy...)
				return policy
			}

			securityPolicies := applyPolicy("testdata/requestauthn/global-jwt.yaml.tmpl", rootNS{})
			ingressCfgs := applyPolicy("testdata/requestauthn/ingress.yaml.tmpl", ns)

			defer g.DeleteConfigOrFail(t, rootNS{}, securityPolicies...)
			defer g.DeleteConfigOrFail(t, ns, ingressCfgs...)

			var a, b echo.Instance
			echoboot.NewBuilderOrFail(ctx, ctx).
				With(&a, util.EchoConfig("a", ns, false, nil, g, p)).
				With(&b, util.EchoConfig("b", ns, false, nil, g, p)).
				BuildOrFail(t)

			// These test cases verify in-mesh traffic doesn't need tokens.
			testCases := []authn.TestCase{
				{
					Name: "in-mesh-with-expired-token",
					Request: connection.Checker{
						From: a,
						Options: echo.CallOptions{
							Target:   b,
							PortName: "http",
							Scheme:   scheme.HTTP,
							Headers: map[string][]string{
								authHeaderKey: {"Bearer " + jwt.TokenExpired},
							},
						},
					},
					ExpectResponseCode: response.StatusUnauthorized,
				},
				{
					Name: "in-mesh-without-token",
					Request: connection.Checker{
						From: a,
						Options: echo.CallOptions{
							Target:   b,
							PortName: "http",
							Scheme:   scheme.HTTP,
						},
					},
					ExpectResponseCode: response.StatusCodeOK,
				},
			}
			for _, c := range testCases {
				t.Run(c.Name, func(t *testing.T) {
					retry.UntilSuccessOrFail(t, c.CheckAuthn,
						retry.Delay(250*time.Millisecond), retry.Timeout(30*time.Second))
				})
			}

			// These test cases verify requests go through ingress will be checked for validate token.
			ingTestCases := []struct {
				Name               string
				Host               string
				Path               string
				Token              string
				ExpectResponseCode int
			}{
				{
					Name:               "deny without token",
					Host:               "example.com",
					Path:               "/",
					ExpectResponseCode: 403,
				},
				{
					Name:               "allow with sub-1 token",
					Host:               "example.com",
					Path:               "/",
					Token:              jwt.TokenIssuer1,
					ExpectResponseCode: 200,
				},
				{
					Name:               "deny with sub-2 token",
					Host:               "example.com",
					Path:               "/",
					Token:              jwt.TokenIssuer2,
					ExpectResponseCode: 403,
				},
				{
					Name:               "deny with expired token",
					Host:               "example.com",
					Path:               "/",
					Token:              jwt.TokenExpired,
					ExpectResponseCode: 401,
				},
				{
					Name:               "allow with sub-1 token on any.com",
					Host:               "any-request-principlal-ok.com",
					Path:               "/",
					Token:              jwt.TokenIssuer1,
					ExpectResponseCode: 200,
				},
				{
					Name:               "allow with sub-2 token on any.com",
					Host:               "any-request-principlal-ok.com",
					Path:               "/",
					Token:              jwt.TokenIssuer2,
					ExpectResponseCode: 200,
				},
				{
					Name:               "deny without token on any.com",
					Host:               "any-request-principlal-ok.com",
					Path:               "/",
					ExpectResponseCode: 403,
				},
				{
					Name:               "deny with token on other host",
					Host:               "other-host.com",
					Path:               "/",
					Token:              jwt.TokenIssuer1,
					ExpectResponseCode: 403,
				},
				{
					Name:               "allow healthz",
					Host:               "example.com",
					Path:               "/healthz",
					ExpectResponseCode: 200,
				},
			}

			for _, c := range ingTestCases {
				t.Run(c.Name, func(t *testing.T) {
					retry.UntilSuccessOrFail(t, func() error {
						return checkIngress(ingr, c.Host, c.Path, c.Token, c.ExpectResponseCode)
					},
						retry.Delay(250*time.Millisecond), retry.Timeout(30*time.Second))
				})
			}
		})
}

func checkIngress(ingr ingress.Instance, host string, path string, token string, expectResponseCode int) error {
	endpointAddress := ingr.HTTPAddress()
	opts := ingress.CallOptions{
		Host:     host,
		Path:     path,
		CallType: ingress.PlainText,
		Address:  endpointAddress,
	}
	if len(token) != 0 {
		opts.Headers = http.Header{
			"Authorization": []string{
				fmt.Sprintf("Bearer %s", token),
			},
		}
	}
	response, err := ingr.Call(opts)

	if response.Code != expectResponseCode {
		return fmt.Errorf("got response code %d, err %s", response.Code, err)
	}
	return nil
}