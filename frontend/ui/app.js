const caseRows = [
  { id: 'BB-1048', source: 'DB sync', owner: 'Anita', status: 'open', priority: 'high' },
  { id: 'BB-1037', source: 'User access', owner: 'Vikram', status: 'progress', priority: 'medium' },
  { id: 'BB-1025', source: 'Audit trail', owner: 'Meera', status: 'resolved', priority: 'low' },
  { id: 'BB-1016', source: 'Case escalations', owner: 'Rohit', status: 'waiting', priority: 'high' },
  { id: 'BB-1009', source: 'Data quality', owner: 'Shruti', status: 'open', priority: 'medium' },
  { id: 'BB-0994', source: 'Attendance sync', owner: 'Nikhil', status: 'resolved', priority: 'low' }
];

const trendData = [32, 38, 54, 46, 63, 58, 72, 64, 89, 78, 92, 84];
const statusData = [
  { name: 'Open', value: 47, color: '#3ad7d0' },
  { name: 'In progress', value: 31, color: '#8b7dff' },
  { name: 'Waiting', value: 22, color: '#ffbf69' }
];

const dashboardState = {
  totalsCase: 1284,
  resolvedCount: 961,
  riskCount: 53,
  healthScore: 98.7
};

const authView = document.getElementById('authView');
const dashboardView = document.getElementById('dashboardView');
const loginForm = document.getElementById('loginForm');
const refreshDashboardButton = document.getElementById('refreshDashboard');
const caseTableBody = document.getElementById('caseTableBody');
const chartBars = document.getElementById('chartBars');
const legendList = document.getElementById('legendList');
const dbStatusList = document.getElementById('dbStatusList');

function setStatusTag(status) {
  const map = {
    open: 'Open',
    progress: 'In progress',
    resolved: 'Resolved',
    waiting: 'Waiting'
  };

  const classes = {
    open: 'open',
    progress: 'progress',
    resolved: 'resolved',
    waiting: 'waiting'
  };

  return `<span class="status-tag ${classes[status]}">${map[status]}</span>`;
}

function setPriorityBadge(priority) {
  const map = {
    high: 'High',
    medium: 'Medium',
    low: 'Low'
  };

  const classes = {
    high: 'high',
    medium: 'medium',
    low: 'low'
  };

  return `<span class="priority-pill ${classes[priority]}">${map[priority]}</span>`;
}

function renderCaseTable() {
  caseTableBody.innerHTML = caseRows
    .map(
      (row) => `
        <tr>
          <td>${row.id}</td>
          <td>${row.source}</td>
          <td>${row.owner}</td>
          <td>${setStatusTag(row.status)}</td>
          <td>${setPriorityBadge(row.priority)}</td>
        </tr>
      `
    )
    .join('');
}

function renderChartBars() {
  chartBars.innerHTML = trendData
    .map(
      (value, index) => `
        <div class="bar-col">
          <div class="bar-track">
            <div class="bar-fill" style="height: ${value}%"></div>
          </div>
          <span class="bar-label">${index + 1}</span>
        </div>
      `
    )
    .join('');
}

function renderLegend() {
  const total = statusData.reduce((sum, item) => sum + item.value, 0);
  legendList.innerHTML = statusData
    .map(
      (item) => `
        <li>
          <span class="meta"><span class="dot" style="background:${item.color};"></span>${item.name}</span>
          <strong>${Math.round((item.value / total) * 100)}%</strong>
        </li>
      `
    )
    .join('');

  const healthyShare = Math.round((statusData[0].value / total) * 100);
  document.getElementById('mixScore').textContent = `${healthyShare}%`;
}

function renderDatabaseStatus() {
  const items = [
    { name: 'Core database', value: 92, label: 'good' },
    { name: 'User sync', value: 76, label: 'warn' },
    { name: 'Case indexing', value: 89, label: 'good' },
    { name: 'Audit retention', value: 67, label: 'warn' }
  ];

  dbStatusList.innerHTML = items
    .map(
      (item) => `
        <div class="db-item">
          <div class="db-item-head">
            <strong>${item.name}</strong>
            <span class="db-pill ${item.label}">${item.value}%</span>
          </div>
          <div class="db-progress">
            <span style="width:${item.value}%"></span>
          </div>
        </div>
      `
    )
    .join('');
}

function renderDashboardStats() {
  document.getElementById('totalsCase').textContent = dashboardState.totalsCase.toLocaleString();
  document.getElementById('resolvedCount').textContent = dashboardState.resolvedCount.toLocaleString();
  document.getElementById('riskCount').textContent = dashboardState.riskCount.toLocaleString();
  document.getElementById('healthScore').textContent = `${dashboardState.healthScore}%`;
}

function toggleAuth(showDashboard) {
  authView.classList.toggle('hidden', showDashboard);
  dashboardView.classList.toggle('hidden', !showDashboard);
}

function setDemoSession() {
  const saved = localStorage.getItem('brainbird_session');
  if (saved === 'active') {
    toggleAuth(true);
  } else {
    toggleAuth(false);
  }
}

loginForm.addEventListener('submit', (event) => {
  event.preventDefault();

  const email = document.getElementById('email').value.trim();
  const password = document.getElementById('password').value.trim();

  if (!email || !password) {
    alert('Please enter valid login details.');
    return;
  }

  if (email === 'admin@brainbird.com' && password === 'admin123') {
    localStorage.setItem('brainbird_session', 'active');
    toggleAuth(true);
    return;
  }

  if (email && password) {
    localStorage.setItem('brainbird_session', 'active');
    toggleAuth(true);
  }
});

refreshDashboardButton.addEventListener('click', () => {
  renderDashboardStats();
  renderCaseTable();
  renderChartBars();
  renderLegend();
  renderDatabaseStatus();
});

function initDashboard() {
  renderDashboardStats();
  renderCaseTable();
  renderChartBars();
  renderLegend();
  renderDatabaseStatus();
}

setDemoSession();
initDashboard();
