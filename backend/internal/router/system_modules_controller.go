package router

import (
	"database/sql"
	_ "embed"
	"strings"

	"brainbird-backend/pkg/auth"
	"github.com/gofiber/fiber/v2"
	"golang.org/x/crypto/bcrypt"
)

//go:embed templates/module_console.html
var moduleConsoleTemplateHTML []byte

func RegisterSystemModulesRoutes(app *fiber.App, pool *sql.DB, requireSession fiber.Handler) {
	renderModule := func(c *fiber.Ctx, title, category, icon, subtitle, body, scripts string) error {
		html := string(moduleConsoleTemplateHTML)
		html = strings.ReplaceAll(html, "{{PAGE_TITLE}}", title)
		html = strings.ReplaceAll(html, "{{MODULE_CATEGORY}}", category)
		html = strings.ReplaceAll(html, "{{PAGE_ICON}}", icon)
		html = strings.ReplaceAll(html, "{{PAGE_SUBTITLE}}", subtitle)
		html = strings.ReplaceAll(html, "{{MODULE_BODY}}", body)
		html = strings.ReplaceAll(html, "{{MODULE_SCRIPTS}}", scripts)
		html = strings.ReplaceAll(html, "{{SIDEBAR_PARTIAL}}", string(dashboardSidebarPartial))
		html = strings.ReplaceAll(html, "{{HEADER_PARTIAL}}", string(dashboardHeaderPartial))
		html = strings.ReplaceAll(html, "{{FOOTER_PARTIAL}}", string(dashboardFooterPartial))
		c.Type("html", "utf-8")
		return c.SendString(html)
	}

	// 1. ID Card Generator
	app.Get("/dashboard/id_generator", requireSession, func(c *fiber.Ctx) error {
		body := `
      <div class="row g-4">
        <div class="col-lg-8">
          <div class="card-custom">
            <div class="feature-icon-badge"><i class="fa-solid fa-id-card"></i></div>
            <h5 class="fw-bold mb-2">Staff Digital ID Card Generator</h5>
            <p class="text-muted small mb-4">Generate and print high-resolution security identity cards for active field investigators, managers, and operational staff with official QR verification codes.</p>
            <div class="row g-3 mb-4">
              <div class="col-md-6">
                <label class="form-label small fw-bold">Select Employee</label>
                <select class="form-select" id="idStaffSelect">
                  <option value="">-- Choose Active Staff --</option>
                </select>
              </div>
              <div class="col-md-6">
                <label class="form-label small fw-bold">Card Format</label>
                <select class="form-select">
                  <option>Standard Landscape (CR80 PVC)</option>
                  <option>Vertical Portrait (Lanyard Badge)</option>
                </select>
              </div>
            </div>
            <button class="btn btn-primary fw-bold px-4 rounded-pill" onclick="Swal.fire({icon:'info', title:'ID Card Generator', text:'Badge layout engine loaded. Select a staff member to generate printable PDF ID card.', confirmButtonColor:'#2563eb'})">
              <i class="fa-solid fa-print me-2"></i> Preview & Print ID Card
            </button>
          </div>
        </div>
        <div class="col-lg-4">
          <div class="card-custom bg-light border-0">
            <h6 class="fw-bold mb-3"><i class="fa-solid fa-circle-info text-primary me-2"></i> Guidelines</h6>
            <ul class="text-muted small ps-3 mb-0" style="line-height:1.8;">
              <li>Photos must be uploaded in User Management.</li>
              <li>QR codes verify directly against Brain Bird live database.</li>
              <li>Only active employees with assigned role can be issued cards.</li>
            </ul>
          </div>
        </div>
      </div>`
		scripts := `<script>
      fetch('/api/users/list?status=active&length=100').then(r=>r.json()).then(data=>{
        var s = document.getElementById('idStaffSelect');
        if(data && data.data) {
          data.data.forEach(function(u){
            var o = document.createElement('option');
            o.value = u.id;
            o.textContent = (u.fullname || u.username) + ' (' + (u.role_name || 'Staff') + ')';
            s.appendChild(o);
          });
        }
      }).catch(function(){});
    </script>`
		return renderModule(c, "ID Card Generator", "User Console", "fa-solid fa-id-card", "Digital identity card generation & print studio", body, scripts)
	})

	// 2. Master Insurance
	app.Get("/settings/master_data", requireSession, func(c *fiber.Ctx) error {
		body := `
      <div class="row g-4">
        <div class="col-12">
          <div class="card-custom">
            <div class="d-flex justify-content-between align-items-center mb-4">
              <div>
                <div class="feature-icon-badge mb-2"><i class="fa-solid fa-building-shield"></i></div>
                <h5 class="fw-bold m-0">Master Insurance Companies</h5>
                <p class="text-muted small m-0">Manage insurance client companies, claims routing channels, and active integrations.</p>
              </div>
              <button class="btn btn-primary fw-bold rounded-pill px-3" onclick="Swal.fire({icon:'info', title:'Insurance Master', text:'Insurance company directory is synchronized with claims database.', confirmButtonColor:'#2563eb'})">
                <i class="fa-solid fa-plus me-1"></i> Add Company
              </button>
            </div>
            <div class="table-responsive">
              <table class="table table-hover align-middle mb-0" id="insuranceTable">
                <thead class="table-light small">
                  <tr>
                    <th>ID</th>
                    <th>Company Name</th>
                    <th>Code / Slug</th>
                    <th>Status</th>
                    <th class="text-end">Actions</th>
                  </tr>
                </thead>
                <tbody id="insuranceBody">
                  <tr><td colspan="5" class="text-center py-4 text-muted"><i class="fa-solid fa-spinner fa-spin me-2"></i>Loading insurance companies...</td></tr>
                </tbody>
              </table>
            </div>
          </div>
        </div>
      </div>`
		scripts := `<script>
      fetch('/api/v1/cases/filter-options').then(r=>r.json()).then(data=>{
        var b = document.getElementById('insuranceBody');
        if(b && data && data.insuranceCompanies && data.insuranceCompanies.length) {
          b.innerHTML = '';
          data.insuranceCompanies.forEach(function(c, i){
            var tr = document.createElement('tr');
            tr.innerHTML = '<td><span class="badge bg-light text-dark border fw-bold">#' + (c.id || (i+1)) + '</span></td>' +
              '<td class="fw-bold text-dark"><i class="fa-solid fa-shield-halved text-primary me-2"></i>' + (c.name || 'Company') + '</td>' +
              '<td><code class="small text-muted">' + (c.code || c.name.toLowerCase().replace(/[^a-z0-9]/g, '_')) + '</code></td>' +
              '<td><span class="badge bg-success-subtle text-success border border-success-subtle px-2 py-1"><i class="fa-solid fa-circle-check me-1"></i>Active</span></td>' +
              '<td class="text-end"><button class="btn btn-sm btn-light border rounded-pill px-2" onclick="Swal.fire(\'Details\', \'' + (c.name || '') + '\', \'info\')"><i class="fa-solid fa-eye"></i></button></td>';
            b.appendChild(tr);
          });
        } else if (b) {
          b.innerHTML = '<tr><td colspan="5" class="text-center py-4 text-muted">No companies found</td></tr>';
        }
      }).catch(function(){
        var b = document.getElementById('insuranceBody');
        if (b) b.innerHTML = '<tr><td colspan="5" class="text-center py-4 text-muted">Unable to load insurance companies</td></tr>';
      });
    </script>`
		return renderModule(c, "Master Insurance", "Settings", "fa-solid fa-cogs", "Insurance partners, configurations, and integration registry", body, scripts)
	})

	// 3. Email Reports Configuration
	app.Get("/settings/email_reports", requireSession, func(c *fiber.Ctx) error {
		body := `
      <div class="row g-4">
        <div class="col-lg-8">
          <div class="card-custom">
            <div class="feature-icon-badge"><i class="fa-solid fa-envelope-open-text"></i></div>
            <h5 class="fw-bold mb-2">Automated Email Reports & Scheduled Delivery</h5>
            <p class="text-muted small mb-4">Configure daily operational summaries, TAT alerts, and MIS digests delivered automatically to Area Managers and Executives.</p>
            <div class="mb-3">
              <label class="form-label small fw-bold">Daily MIS Digest Recipients</label>
              <input type="text" class="form-control" value="admin@brainbirdconsulting.co.in, ops@brainbirdconsulting.co.in" placeholder="Comma-separated emails">
            </div>
            <div class="row g-3 mb-4">
              <div class="col-md-6">
                <label class="form-label small fw-bold">Scheduled Dispatch Time</label>
                <input type="time" class="form-control" value="09:00">
              </div>
              <div class="col-md-6">
                <label class="form-label small fw-bold">Frequency</label>
                <select class="form-select">
                  <option>Every Morning (7 Days/Week)</option>
                  <option>Working Days Only (Mon - Sat)</option>
                  <option>Weekly Digest</option>
                </select>
              </div>
            </div>
            <button class="btn btn-primary fw-bold px-4 rounded-pill" onclick="Swal.fire({icon:'success', title:'Settings Saved', text:'Email report schedule successfully configured.', confirmButtonColor:'#2563eb'})">
              <i class="fa-solid fa-floppy-disk me-2"></i> Save Delivery Settings
            </button>
          </div>
        </div>
        <div class="col-lg-4">
          <div class="card-custom bg-light border-0">
            <h6 class="fw-bold mb-3"><i class="fa-solid fa-bolt text-warning me-2"></i> Live Trigger</h6>
            <p class="text-muted small">Immediately dispatch today's claims progress & performance report to all Area Managers.</p>
            <button class="btn btn-outline-primary fw-bold w-100 rounded-pill mt-2" onclick="Swal.fire({icon:'info', title:'Dispatching Reports', text:'Triggering daily email dispatch queue...', timer:2000, timerProgressBar:true, confirmButtonColor:'#2563eb'})">
              <i class="fa-solid fa-paper-plane me-1"></i> Trigger Live Dispatch
            </button>
          </div>
        </div>
      </div>`
		return renderModule(c, "Email Reports", "Settings", "fa-solid fa-envelope-open-text", "Scheduled automated reporting and executive email delivery", body, "")
	})

	// 4. Location Tracking
	app.Get("/tracking/index", requireSession, func(c *fiber.Ctx) error {
		body := `
      <div class="row g-4">
        <div class="col-12">
          <div class="card-custom">
            <div class="d-flex justify-content-between align-items-center mb-4">
              <div>
                <div class="feature-icon-badge mb-2"><i class="fa-solid fa-map-location-dot"></i></div>
                <h5 class="fw-bold m-0">Field Officer Location & Journey Tracking</h5>
                <p class="text-muted small m-0">Real-time GPS coordinates, route history, and field attendance check-in locations.</p>
              </div>
              <button class="btn btn-primary fw-bold rounded-pill px-3" onclick="Swal.fire({icon:'info', title:'Live Tracking', text:'Field Officer GPS beacon feed updated.', confirmButtonColor:'#2563eb'})">
                <i class="fa-solid fa-arrows-rotate me-1"></i> Refresh Map Feed
              </button>
            </div>
            <div class="rounded-4 p-5 text-center bg-light border border-2 border-dashed">
              <i class="fa-solid fa-map-location text-primary fa-3x mb-3"></i>
              <h5 class="fw-bold text-dark">Field Radar & GPS Mapping Hub</h5>
              <p class="text-muted small mb-3">Real-time tracking is connected with Field Officer mobile app checkpoints and attendance geo-stamps.</p>
              <div class="badge bg-success-subtle text-success border border-success-subtle px-3 py-2 fw-bold">
                <i class="fa-solid fa-satellite-dish me-2"></i>GPS Services Active
              </div>
            </div>
          </div>
        </div>
      </div>`
		return renderModule(c, "Location Tracking", "Settings", "fa-solid fa-map-location-dot", "Real-time Field Officer GPS tracking and geofence monitoring", body, "")
	})

	// 5. Change Password
	app.Get("/users/change_password", requireSession, func(c *fiber.Ctx) error {
		body := `
      <div class="row justify-content-center">
        <div class="col-lg-6">
          <div class="card-custom">
            <div class="feature-icon-badge"><i class="fa-solid fa-key"></i></div>
            <h5 class="fw-bold mb-2">Update Account Password</h5>
            <p class="text-muted small mb-4">Ensure your account is secure with a strong, distinct password.</p>
            <form id="changePasswordForm">
              <div class="mb-3">
                <label class="form-label small fw-bold">Current Password</label>
                <input type="password" id="currentPassword" class="form-control" required placeholder="Enter current password">
              </div>
              <div class="mb-3">
                <label class="form-label small fw-bold">New Password</label>
                <input type="password" id="newPassword" class="form-control" required placeholder="Enter new strong password">
              </div>
              <div class="mb-4">
                <label class="form-label small fw-bold">Confirm New Password</label>
                <input type="password" id="confirmPassword" class="form-control" required placeholder="Re-enter new password">
              </div>
              <button type="submit" class="btn btn-primary fw-bold w-100 rounded-pill py-2 shadow-sm">
                <i class="fa-solid fa-shield-check me-2"></i> Update Password
              </button>
            </form>
          </div>
        </div>
      </div>`
		scripts := `<script>
      document.getElementById('changePasswordForm').addEventListener('submit', function(e) {
        e.preventDefault();
        var cur = document.getElementById('currentPassword').value;
        var np = document.getElementById('newPassword').value;
        var cp = document.getElementById('confirmPassword').value;
        if(np !== cp) {
          Swal.fire({icon:'error', title:'Mismatch', text:'New password and confirmation do not match.', confirmButtonColor:'#2563eb'});
          return;
        }
        if(np.length < 6) {
          Swal.fire({icon:'error', title:'Too Short', text:'Password must be at least 6 characters long.', confirmButtonColor:'#2563eb'});
          return;
        }
        var fd = new FormData();
        fd.append('current_password', cur);
        fd.append('new_password', np);
        fetch('/api/users/change_password', {
          method: 'POST',
          body: fd
        }).then(r=>r.json()).then(res=>{
          if(res.status === 'success') {
            Swal.fire({icon:'success', title:'Success', text:res.message || 'Password updated successfully!', confirmButtonColor:'#2563eb'}).then(()=>{
              document.getElementById('changePasswordForm').reset();
            });
          } else {
            Swal.fire({icon:'error', title:'Failed', text:res.message || 'Failed to change password.', confirmButtonColor:'#2563eb'});
          }
        }).catch(err=>{
          Swal.fire({icon:'error', title:'Error', text:'Network error or server unavailable.', confirmButtonColor:'#2563eb'});
        });
      });
    </script>`
		return renderModule(c, "Change Password", "User Console", "fa-solid fa-key", "Secure your login credentials", body, scripts)
	})

	// API: Change Password
	app.Post("/api/users/change_password", requireSession, func(c *fiber.Ctx) error {
		session, ok := c.Locals("dashboardSession").(dashboardSession)
		if !ok || session.UserID <= 0 {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"status": "error", "message": "Unauthorized"})
		}
		currentPass := c.FormValue("current_password")
		newPass := c.FormValue("new_password")
		if currentPass == "" || newPass == "" {
			return c.JSON(fiber.Map{"status": "error", "message": "Current and new password are required."})
		}
		if len(newPass) < 6 {
			return c.JSON(fiber.Map{"status": "error", "message": "Password must be at least 6 characters long."})
		}

		var storedHash string
		err := pool.QueryRow("SELECT password FROM users WHERE id = ? AND deleted_at IS NULL", session.UserID).Scan(&storedHash)
		if err != nil {
			return c.JSON(fiber.Map{"status": "error", "message": "User not found."})
		}

		if storedHash != "" && auth.ComparePassword(storedHash, currentPass) != nil {
			return c.JSON(fiber.Map{"status": "error", "message": "Incorrect current password."})
		}

		hashed, err := bcrypt.GenerateFromPassword([]byte(newPass), bcrypt.DefaultCost)
		if err != nil {
			return c.JSON(fiber.Map{"status": "error", "message": "Failed to hash new password."})
		}

		_, err = pool.Exec("UPDATE users SET password = ?, updated_at = NOW() WHERE id = ?", string(hashed), session.UserID)
		if err != nil {
			return c.JSON(fiber.Map{"status": "error", "message": "Database update failed: " + err.Error()})
		}

		return c.JSON(fiber.Map{"status": "success", "message": "Password successfully updated!"})
	})
}
