package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// Exercise the same policy through native bearer authentication and browser
// cookies. Cookie writes deliberately carry a valid Origin so a CSRF rejection
// cannot accidentally make an account-policy test pass.
func policyRequest(t *testing.T, env *testEnv, mode, token, method, path, body string, headers map[string]string, want int) ([]byte, http.Header) {
	t.Helper()
	req, err := http.NewRequest(method, env.url("/api/v1"+path), strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if mode == "cookie" {
		req.AddCookie(&http.Cookie{Name: "psst_session", Value: token})
		req.Header.Set("Origin", env.server.URL)
	} else if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := env.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != want {
		t.Fatalf("%s %s %s: want %d, got %d: %s", mode, method, path, want, resp.StatusCode, data)
	}
	return data, resp.Header
}

func policyCode(t *testing.T, data []byte, want string) {
	t.Helper()
	var response struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		t.Fatal(err)
	}
	if response.Code != want {
		t.Fatalf("wanted restriction %q, got %s", want, data)
	}
}

func TestAccountPolicyExistingSessionCannotContinueUpload(t *testing.T) {
	for _, mode := range []string{"bearer", "cookie"} {
		for _, restriction := range []struct{ name, sql, code string }{
			{"administrator", `UPDATE users SET role='admin' WHERE id=?`, "admin_transfer_forbidden"},
			{"temporary-password", `UPDATE users SET must_change_password=1 WHERE id=?`, "password_change_required"},
		} {
			t.Run(mode+"/"+restriction.name, func(t *testing.T) {
				env := setupAuthFixture(t, false)
				user, token := addAccount(t, env, "owner", "user")
				transfer := authRequest(t, env, "POST", "/transfers", token, nil, 201)["id"].(string)
				slot := authRequest(t, env, "POST", "/slots", token, nil, 201)["id"].(string)
				_, headers := policyRequest(t, env, mode, token, "POST", "/transfers/"+transfer+"/files", "", map[string]string{"Tus-Resumable": "1.0.0", "Upload-Length": "4"}, 201)
				upload := strings.TrimPrefix(headers.Get("Location"), "/api/v1")
				if _, err := env.db.Exec(restriction.sql, user.ID); err != nil {
					t.Fatal(err)
				}
				routes := []struct{ method, path, body string }{
					{"POST", "/transfers", ""}, {"POST", "/slots", ""},
					{"POST", "/transfers/" + transfer + "/files", ""},
					{"PATCH", upload, "data"}, {"HEAD", upload, ""},
					{"POST", "/transfers/" + transfer + "/manifest", "opaque"},
					{"POST", "/transfers/" + transfer + "/complete", ""},
					{"POST", "/auth/pairings", ""}, {"GET", "/auth/resources", ""},
				}
				for _, route := range routes {
					data, _ := policyRequest(t, env, mode, token, route.method, route.path, route.body, map[string]string{"Tus-Resumable": "1.0.0", "Upload-Length": "4", "Upload-Offset": "0", "Content-Type": "application/offset+octet-stream"}, 403)
					if route.method != "HEAD" {
						policyCode(t, data, restriction.code)
					}
				}
				var count int
				if err := env.db.QueryRow(`SELECT count(*) FROM files WHERE transfer_id=?`, transfer).Scan(&count); err != nil || count != 1 {
					t.Fatalf("blocked POST created a file: %d %v", count, err)
				}
				var offset int
				if err := env.db.QueryRow(`SELECT upload_offset FROM files WHERE transfer_id=?`, transfer).Scan(&offset); err != nil || offset != 0 {
					t.Fatalf("blocked PATCH wrote data: %d %v", offset, err)
				}
				tr, err := env.queries.GetTransfer(transfer)
				if err != nil || tr.Status != "pending" {
					t.Fatalf("blocked completion changed status: %+v %v", tr, err)
				}
				exists, err := env.queries.HasManifest(transfer)
				if err != nil || exists {
					t.Fatalf("blocked manifest accepted: %v %v", exists, err)
				}
				// Existing public links are still readable and usable for invited uploads.
				policyRequest(t, env, mode, token, "GET", "/transfers/"+transfer, "", nil, 200)
				policyRequest(t, env, mode, token, "POST", "/slots/"+slot+"/transfers", "", nil, 201)
				if restriction.name == "administrator" {
					policyRequest(t, env, mode, token, "GET", "/auth/resources?all=true", "", nil, 200)
					policyRequest(t, env, mode, token, "GET", "/auth/sessions", "", nil, 200)
					policyRequest(t, env, mode, token, "DELETE", "/transfers/"+transfer, "", nil, 204)
					policyRequest(t, env, mode, token, "DELETE", "/slots/"+slot, "", nil, 204)
				} else {
					for _, route := range []struct{ method, path string }{{"GET", "/auth/sessions"}, {"DELETE", "/auth/sessions/owner-session-id"}, {"GET", "/auth/resources?all=true"}, {"GET", "/auth/pairings/nonexistent"}, {"DELETE", "/auth/pairings/nonexistent"}} {
						data, _ := policyRequest(t, env, mode, token, route.method, route.path, "", nil, 403)
						policyCode(t, data, "password_change_required")
					}
					policyRequest(t, env, mode, token, "DELETE", "/transfers/"+transfer, "", nil, 403)
					policyRequest(t, env, mode, token, "DELETE", "/slots/"+slot, "", nil, 403)
				}
			})
		}
	}
}

func TestAccountPolicyPublicCapabilityIgnoresAmbientAccountRestriction(t *testing.T) {
	for _, role := range []string{"admin", "restricted"} {
		t.Run(role, func(t *testing.T) {
			env := setupAuthFixture(t, false)
			user, token := addAccount(t, env, "visitor", "user")
			slot := authRequest(t, env, "POST", "/slots", env.userToken, nil, 201)["id"].(string)
			if role == "admin" {
				if _, err := env.db.Exec(`UPDATE users SET role='admin' WHERE id=?`, user.ID); err != nil {
					t.Fatal(err)
				}
			} else if _, err := env.db.Exec(`UPDATE users SET must_change_password=1 WHERE id=?`, user.ID); err != nil {
				t.Fatal(err)
			}
			raw, _ := policyRequest(t, env, "cookie", token, "POST", "/slots/"+slot+"/transfers", "", nil, 201)
			var child struct {
				ID    string `json:"id"`
				Token string `json:"delete_token"`
			}
			if err := json.Unmarshal(raw, &child); err != nil {
				t.Fatal(err)
			}
			base := "/transfers/" + child.ID
			capability := map[string]string{"Authorization": "Bearer " + child.Token, "Tus-Resumable": "1.0.0", "Upload-Length": "4"}
			_, headers := policyRequest(t, env, "cookie", token, "POST", base+"/files", "", capability, 201)
			upload := strings.TrimPrefix(headers.Get("Location"), "/api/v1")
			policyRequest(t, env, "cookie", token, "HEAD", upload, "", capability, 200)
			capability["Upload-Offset"] = "0"
			capability["Content-Type"] = "application/offset+octet-stream"
			policyRequest(t, env, "cookie", token, "PATCH", upload, "data", capability, 204)
			policyRequest(t, env, "cookie", token, "POST", base+"/manifest", "opaque", capability, 204)
			policyRequest(t, env, "cookie", token, "POST", base+"/complete", "", capability, 204)
			for _, path := range []string{base, base + "/manifest", upload} {
				policyRequest(t, env, "cookie", token, "GET", path, "", nil, 200)
			}
			policyRequest(t, env, "cookie", token, "POST", base+"/downloaded", "", nil, 204)
		})
	}
}

func TestAccountPolicyFirstPasswordChangeAndAdminExemption(t *testing.T) {
	for _, mode := range []string{"bearer", "cookie"} {
		t.Run(mode, func(t *testing.T) {
			env := setupAuthFixture(t, false)
			created := authRequest(t, env, "POST", "/admin/users", env.authToken, map[string]string{"username": "newperson", "password": "temporary password"}, 201)
			if created["user"].(map[string]any)["must_change_password"] != true {
				t.Fatalf("new user unrestricted: %v", created)
			}
			sessionType := "device"
			if mode == "cookie" {
				sessionType = "web"
			}
			raw, headers := policyRequest(t, env, "", "", "POST", "/auth/login", `{"username":"newperson","password":"temporary password","session_type":"`+sessionType+`"}`, map[string]string{"Origin": env.server.URL}, 200)
			var login struct {
				Token string `json:"token"`
				User  struct {
					MustChange bool `json:"must_change_password"`
				} `json:"user"`
			}
			if err := json.Unmarshal(raw, &login); err != nil {
				t.Fatal(err)
			}
			if !login.User.MustChange {
				t.Fatal("login omitted mandatory-password flag")
			}
			token := login.Token
			if mode == "cookie" {
				response := http.Response{Header: headers}
				if cookies := response.Cookies(); len(cookies) == 1 {
					token = cookies[0].Value
				} else {
					t.Fatalf("cookies: %v", cookies)
				}
			}
			policyRequest(t, env, mode, token, "GET", "/auth/me", "", nil, 200)
			for _, body := range []string{`{"current_password":"temporary password","password":"temporary password"}`, `{"current_password":"temporary password","password":"short"}`} {
				policyRequest(t, env, mode, token, "POST", "/auth/password", body, nil, 400)
			}
			policyRequest(t, env, mode, token, "POST", "/auth/password", `{"current_password":"incorrect current","password":"personal new password"}`, nil, 403)
			data, _ := policyRequest(t, env, mode, token, "POST", "/transfers", "", nil, 403)
			policyCode(t, data, "password_change_required")
			policyRequest(t, env, mode, token, "POST", "/auth/password", `{"current_password":"temporary password","password":"personal new password"}`, nil, 204)
			policyRequest(t, env, mode, token, "GET", "/auth/me", "", nil, 401)
			signedIn := authRequest(t, env, "POST", "/auth/login", "", map[string]string{"username": "newperson", "password": "personal new password", "session_type": "device"}, 200)
			if signedIn["user"].(map[string]any)["must_change_password"] != false {
				t.Fatal("successful change retained restriction")
			}
			authRequest(t, env, "POST", "/transfers", signedIn["token"].(string), nil, 201)
			admin := authRequest(t, env, "POST", "/admin/users", env.authToken, map[string]string{"username": "newadmin", "password": "administrator password", "role": "admin"}, 201)
			if admin["user"].(map[string]any)["must_change_password"] != false {
				t.Fatal("administrator forced to change initial password")
			}
		})
	}
}

func TestAccountPolicyRestrictedLogout(t *testing.T) {
	for _, mode := range []string{"bearer", "cookie"} {
		t.Run(mode, func(t *testing.T) {
			env := setupAuthFixture(t, false)
			user, token := addAccount(t, env, "temporary", "user")
			if _, err := env.db.Exec(`UPDATE users SET must_change_password=1 WHERE id=?`, user.ID); err != nil {
				t.Fatal(err)
			}
			policyRequest(t, env, mode, token, "POST", "/auth/logout", "", nil, 204)
			policyRequest(t, env, mode, token, "GET", "/auth/me", "", nil, 401)
		})
	}
}

func TestAccountPolicyOutstandingPairingCannotBypassNewRestriction(t *testing.T) {
	for _, restriction := range []struct{ name, sql, code string }{
		{"administrator", `UPDATE users SET role='admin' WHERE id=?`, "admin_transfer_forbidden"},
		{"temporary-password", `UPDATE users SET must_change_password=1 WHERE id=?`, "password_change_required"},
	} {
		t.Run(restriction.name, func(t *testing.T) {
			env := setupAuthFixture(t, false)
			user, token := addAccount(t, env, "issuer", "user")
			pairing := authRequest(t, env, "POST", "/auth/pairings", token, nil, 201)
			if _, err := env.db.Exec(restriction.sql, user.ID); err != nil {
				t.Fatal(err)
			}
			denied := authRequest(t, env, "POST", "/auth/pairings/redeem", "", map[string]string{"code": pairing["code"].(string)}, 403)
			if denied["code"] != restriction.code {
				t.Fatalf("wrong restriction: %v", denied)
			}
			var count int
			if err := env.db.QueryRow(`SELECT count(*) FROM sessions WHERE user_id=?`, user.ID).Scan(&count); err != nil || count != 1 {
				t.Fatalf("denied redemption created session: %d %v", count, err)
			}
			var status string
			if err := env.db.QueryRow(`SELECT status FROM pairings WHERE user_id=?`, user.ID).Scan(&status); err != nil || status != "pending" {
				t.Fatalf("denied redemption consumed grant: %s %v", status, err)
			}
		})
	}
}

func TestAccountPolicyAdministratorResetRequiresFreshPassword(t *testing.T) {
	env := setupAuthFixture(t, false)
	user, oldToken := addAccount(t, env, "resetperson", "user")
	grant := authRequest(t, env, "POST", "/auth/pairings", oldToken, nil, 201)["code"].(string)
	reset := authRequest(t, env, "PATCH", "/admin/users/"+user.ID, env.authToken, map[string]string{"password": "temporary replacement"}, 200)
	if reset["user"].(map[string]any)["must_change_password"] != true {
		t.Fatalf("reset response omitted restriction: %v", reset)
	}
	authRequest(t, env, "GET", "/auth/me", oldToken, nil, 401)
	authRequest(t, env, "POST", "/auth/pairings/redeem", "", map[string]string{"code": grant}, 401)
	login := authRequest(t, env, "POST", "/auth/login", "", map[string]string{"username": user.Username, "password": "temporary replacement", "session_type": "device"}, 200)
	token := login["token"].(string)
	if login["user"].(map[string]any)["must_change_password"] != true {
		t.Fatal("reset-password login unrestricted")
	}
	for _, route := range []struct{ method, path string }{{"POST", "/slots"}, {"POST", "/auth/pairings"}, {"GET", "/auth/resources"}, {"GET", "/auth/sessions"}} {
		denied := authRequest(t, env, route.method, route.path, token, nil, 403)
		if denied["code"] != "password_change_required" {
			t.Fatalf("wrong restriction: %v", denied)
		}
	}
	// Validation counts UTF-8 bytes, and unsuccessful attempts must keep both the
	// restricted session and the mandatory-change flag intact.
	for _, password := range []string{strings.Repeat("x", 11), strings.Repeat("é", 37)} {
		authRequest(t, env, "POST", "/auth/password", token, map[string]string{"current_password": "temporary replacement", "password": password}, 400)
		me := authRequest(t, env, "GET", "/auth/me", token, nil, 200)
		if me["user"].(map[string]any)["must_change_password"] != true {
			t.Fatal("invalid password cleared restriction")
		}
	}
	authRequest(t, env, "POST", "/auth/password", token, map[string]string{"current_password": "temporary replacement", "password": "personal replacement"}, 204)
	authRequest(t, env, "GET", "/auth/me", token, nil, 401)
	login = authRequest(t, env, "POST", "/auth/login", "", map[string]string{"username": user.Username, "password": "personal replacement", "session_type": "device"}, 200)
	token = login["token"].(string)
	authRequest(t, env, "POST", "/slots", token, nil, 201)
	authRequest(t, env, "POST", "/auth/pairings", token, nil, 201)
}
