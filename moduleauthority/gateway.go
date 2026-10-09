package moduleauthority

import (
	"fmt"

	"connectrpc.com/connect"
	"github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1/accountsv1connect"
)

// NewGatewayAuthority uses the host's operation-authority bridge instead of a
// direct accounts connection. It supports ExchangeOperation, runtime-boundary
// verification, audit emission and audit receipt lookup, plus the ordinary
// broker mints. The selected host must implement /modules/_authority; there is
// no fallback to direct accounts access or another authority surface.
//
// Authority must be empty. Gateway, credentials and transport protections are
// validated exactly as for New. The bridge retains the existing host decisions;
// choosing this transport grants no additional capability.
func NewGatewayAuthority(seams Seams, credentials Credentials) (*Client, error) {
	if seams.Authority != (Authority{}) {
		return nil, fmt.Errorf("%w: gateway authority must not also select a direct endpoint", ErrInvalidSeams)
	}
	c, err := New(seams, credentials)
	if err != nil {
		return nil, err
	}
	c.authority = authorityEndpoint{
		capabilities: accountsv1connect.NewModuleCapabilitiesServiceClient(
			c.broker.client, c.broker.baseURL+"/modules/_authority",
			connect.WithProtoJSON(), connect.WithReadMaxBytes(maxBrokerResponse),
		),
		token: seams.InternalToken,
	}
	return c, nil
}
