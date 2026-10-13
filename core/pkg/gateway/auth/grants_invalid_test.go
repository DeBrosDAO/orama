package auth

import (
	"errors"
	"testing"
)

// A grant that is wrong as asked is the caller's to fix, and a caller can only
// tell it from a registry failure if the error says so: the app-grant route
// answered 500 for all of these.
func TestGrant_invalidRequestsAreTypedInvalid(t *testing.T) {
	s, _, _ := realRegistry(t)
	for name, req := range map[string]GrantRequest{
		"ownership":                        {Role: RoleOwner},
		"a selector that won't parse":      {Role: RoleRuntime, Resource: "this is not a selector"},
		"a selector the role cannot reach": {Role: RoleRuntime, Resource: "db:table=posts"},
		"a selector nothing applies":       {Role: RoleAdmin, Resource: "db:table=posts:read"},
	} {
		t.Run(name, func(t *testing.T) {
			req.Namespace, req.PrincipalType, req.Identifier = "anchat", PrincipalApp, "app:anchat/web"
			err := s.Grant(t.Context(), req)
			var invalid *ErrInvalidGrant
			if !errors.As(err, &invalid) {
				t.Fatalf("Grant(%+v) = %v, want an *ErrInvalidGrant", req, err)
			}
		})
	}
}

func TestGrant_aValidRequestIsNotInvalid(t *testing.T) {
	s, _, _ := realRegistry(t)
	err := s.Grant(t.Context(), GrantRequest{
		Namespace: "anchat", PrincipalType: PrincipalApp, Identifier: "app:anchat/web",
		Role: RoleRuntime, Resource: "pubsub:topic=jobs.*",
	})
	if err != nil {
		t.Fatalf("a runtime grant narrowed to a topic: %v", err)
	}
}
