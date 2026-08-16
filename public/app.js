const ICONS = {
    wave: `<svg viewBox="0 0 24 24" width="20" height="20" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="22 12 18 12 15 21 9 3 6 12 2 12"></polyline></svg>`,
    download: `<svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"></path><polyline points="7 10 12 15 17 10"></polyline><line x1="12" y1="15" x2="12" y2="3"></line></svg>`,
    upload: `<svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"></path><polyline points="17 8 12 3 7 8"></polyline><line x1="12" y1="3" x2="12" y2="15"></line></svg>`
};

const HISTORY_RANGES = [
    { key: 'today', label: 'Hoy (24h)' },
    { key: '5d', label: '5 días' },
    { key: '7d', label: '7 días' },
    { key: '30d', label: '30 días' },
    { key: 'month', label: 'Mensual' }
];

let config = { interfaces: [], poll_interval_ms: 1000, alert_threshold: 0, history_size: 60 };
let charts = {};
let currentRange = '5d';
let pollCount = 0;

function formatBytes(bytes, decimals = 2) {
    if (!+bytes) return '0 B';
    const k = 1024;
    const dm = decimals < 0 ? 0 : decimals;
    const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return `${parseFloat((bytes / Math.pow(k, i)).toFixed(dm))} ${sizes[i]}`;
}

function formatSpeed(bps) {
    return formatBytes(bps) + '/s';
}

function historyKey(iface) {
    return 'nd-history-' + iface;
}

function loadHistoryFromStorage(iface) {
    try {
        const raw = localStorage.getItem(historyKey(iface));
        const arr = JSON.parse(raw);
        return Array.isArray(arr) ? arr : [];
    } catch (e) {
        return [];
    }
}

function saveHistoryToStorage(iface, samples) {
    try {
        localStorage.setItem(historyKey(iface), JSON.stringify(samples.slice(-config.history_size)));
    } catch (e) { /* storage full or unavailable */ }
}

async function init() {
    const res = await fetch('/api/config');
    config = await res.json();

    buildSummarySection();
    buildSpeedtestSection();
    config.interfaces.forEach(iface => buildInterfaceSections(iface));
    buildRangeTabs();

    pollRealtime();
    loadHistory(currentRange);
    refreshSummary();
    registerSW();
}

function buildSummarySection() {
    const container = document.getElementById('summary-container');
    const cards = [
        { id: 'sum-rx-speed', label: 'Descarga ahora', icon: ICONS.download, cls: 'rx', value: '0 B/s' },
        { id: 'sum-tx-speed', label: 'Subida ahora', icon: ICONS.upload, cls: 'tx', value: '0 B/s' },
        { id: 'sum-rx-today', label: 'Descargado hoy', icon: ICONS.download, cls: 'rx', value: '0 B' },
        { id: 'sum-tx-today', label: 'Subido hoy', icon: ICONS.upload, cls: 'tx', value: '0 B' }
    ];
    cards.forEach(card => {
        const box = document.createElement('div');
        box.className = 'summary-box';
        box.innerHTML = `
            <div class="stat-label">${card.icon} ${card.label}</div>
            <div class="summary-value ${card.cls}" id="${card.id}">${card.value}</div>
        `;
        container.appendChild(box);
    });
}

function buildInterfaceSections(iface) {
    const realtimeContainer = document.getElementById('realtime-container');
    const historyContainer = document.getElementById('history-container');

    const rtSection = document.createElement('div');
    rtSection.className = 'interface-section';
    rtSection.id = `section-${iface}`;
    rtSection.innerHTML = `
        <div class="interface-header-wrap">
            ${ICONS.wave}
            <h2 class="interface-header">${iface}</h2>
            <span class="status-dot" id="status-${iface}" title="Estado desconocido"></span>
            <span class="status-text" id="status-text-${iface}"></span>
        </div>
        <div class="stats-grid">
            <div class="stat-box">
                <div class="stat-info">
                    <div class="stat-label rx">${ICONS.download} Received</div>
                    <div class="stat-value rx" id="rx-speed-${iface}">0 B/s</div>
                </div>
            </div>
            <div class="stat-box">
                <div class="stat-info">
                    <div class="stat-label tx">${ICONS.upload} Sent</div>
                    <div class="stat-value tx" id="tx-speed-${iface}">0 B/s</div>
                </div>
            </div>
        </div>
        <div class="chart-wrapper">
            <canvas id="live-chart-${iface}"></canvas>
        </div>
    `;
    realtimeContainer.appendChild(rtSection);

    const histSection = document.createElement('div');
    histSection.className = 'interface-section';
    histSection.innerHTML = `
        <div class="interface-header-wrap">
            ${ICONS.wave}
            <h2 class="interface-header">${iface} History</h2>
        </div>
        <div id="history-${iface}">Cargando...</div>
    `;
    historyContainer.appendChild(histSection);

    const saved = loadHistoryFromStorage(iface);
    const labels = Array(Math.max(saved.length, 1)).fill('');
    const rxData = saved.length ? saved.map(s => s.rx) : [0];
    const txData = saved.length ? saved.map(s => s.tx) : [0];

    const ctx = document.getElementById(`live-chart-${iface}`).getContext('2d');
    charts[iface] = new Chart(ctx, {
        type: 'line',
        data: {
            labels: labels,
            datasets: [
                {
                    label: 'RX',
                    data: rxData,
                    borderColor: '#3fb950',
                    backgroundColor: 'rgba(63, 185, 80, 0.15)',
                    fill: true,
                    tension: 0.4,
                    pointRadius: 0,
                    borderWidth: 2
                },
                {
                    label: 'TX',
                    data: txData,
                    borderColor: '#58a6ff',
                    backgroundColor: 'rgba(88, 166, 255, 0.15)',
                    fill: true,
                    tension: 0.4,
                    pointRadius: 0,
                    borderWidth: 2
                }
            ]
        },
        options: {
            responsive: true,
            animation: false,
            interaction: { intersect: false, mode: 'index' },
            scales: {
                y: {
                    beginAtZero: true,
                    grid: { color: '#30363d' },
                    ticks: {
                        callback: function (value) { return formatBytes(value); },
                        color: '#8b949e'
                    }
                },
                x: { display: false }
            },
            plugins: {
                legend: { display: true, position: 'top', labels: { color: '#c9d1d9', font: { family: 'sans-serif', size: 12 } } },
                tooltip: {
                    callbacks: {
                        label: function (context) { return context.dataset.label + ': ' + formatBytes(context.parsed.y) + '/s'; }
                    }
                }
            }
        }
    });
}

function buildSpeedtestSection() {
    const section = document.createElement('section');
    section.className = 'dashboard-section';
    section.innerHTML = `
        <h2 class="section-main-title">Speed Test</h2>
        <div class="speedtest-box">
            <div class="speedtest-metrics">
                <div class="speedtest-metric">
                    <div class="stat-label rx">${ICONS.download} Descarga</div>
                    <div class="speedtest-value rx" id="st-download">—</div>
                </div>
                <div class="speedtest-metric">
                    <div class="stat-label tx">${ICONS.upload} Subida</div>
                    <div class="speedtest-value tx" id="st-upload">—</div>
                </div>
                <div class="speedtest-metric">
                    <div class="stat-label">Ping</div>
                    <div class="speedtest-value" id="st-ping">—</div>
                </div>
            </div>
            <div class="speedtest-footer">
                <button id="speedtest-btn" class="speedtest-btn">Iniciar Speed Test</button>
                <span class="speedtest-status" id="speedtest-status">Mide la velocidad máxima de la conexión del servidor (~10 s)</span>
            </div>
        </div>
    `;
    document.querySelector('.dashboard-section').after(section);
    document.getElementById('speedtest-btn').addEventListener('click', runSpeedtest);
}

let speedtestRunning = false;

function formatMbps(bps) {
    if (!bps || bps <= 0) return '—';
    const mbps = bps / 1e6;
    return mbps >= 100 ? Math.round(mbps) + ' Mbps' : mbps.toFixed(1) + ' Mbps';
}

async function runSpeedtest() {
    if (speedtestRunning) return;
    speedtestRunning = true;
    const btn = document.getElementById('speedtest-btn');
    const status = document.getElementById('speedtest-status');
    btn.disabled = true;
    btn.textContent = 'Probando...';
    status.textContent = 'Midiendo descarga y subida, espera unos segundos...';
    try {
        const res = await fetch('/api/speedtest');
        if (!res.ok) {
            const data = await res.json().catch(() => ({}));
            throw new Error(data.error || ('HTTP ' + res.status));
        }
        const data = await res.json();
        document.getElementById('st-download').textContent = formatMbps(data.download.bps);
        document.getElementById('st-upload').textContent = formatMbps(data.upload.bps);
        document.getElementById('st-ping').textContent = data.ping_ms >= 0 ? data.ping_ms + ' ms' : '—';
        status.textContent = 'Completado. Resultados de la conexión del servidor.';
    } catch (e) {
        status.textContent = 'Error: ' + e.message;
    } finally {
        speedtestRunning = false;
        btn.disabled = false;
        btn.textContent = 'Iniciar Speed Test';
    }
}

function buildRangeTabs() {
    const container = document.getElementById('range-tabs');
    HISTORY_RANGES.forEach(rng => {
        const btn = document.createElement('button');
        btn.className = 'tab-btn' + (rng.key === currentRange ? ' active' : '');
        btn.textContent = rng.label;
        btn.dataset.range = rng.key;
        btn.addEventListener('click', () => {
            currentRange = rng.key;
            container.querySelectorAll('.tab-btn').forEach(b => b.classList.remove('active'));
            btn.classList.add('active');
            loadHistory(currentRange);
        });
        container.appendChild(btn);
    });
}

async function pollRealtime() {
    try {
        pollCount++;
        const res = await fetch('/api/realtime');
        const data = await res.json();

        let totalRxSpeed = 0, totalTxSpeed = 0;
        const shouldPersist = pollCount % 10 === 0;

        data.interfaces.forEach(st => {
            const iface = st.name;

            document.getElementById(`rx-speed-${iface}`).innerText = formatSpeed(st.rx_speed);
            document.getElementById(`tx-speed-${iface}`).innerText = formatSpeed(st.tx_speed);

            const dot = document.getElementById(`status-${iface}`);
            const statusText = document.getElementById(`status-text-${iface}`);
            if (st.up) {
                dot.classList.add('up');
                dot.classList.remove('down');
                statusText.textContent = 'Online';
                statusText.style.color = '#3fb950';
            } else {
                dot.classList.add('down');
                dot.classList.remove('up');
                statusText.textContent = st.operstate || 'Offline';
                statusText.style.color = '#f85149';
            }

            const section = document.getElementById(`section-${iface}`);
            const active = config.alert_threshold > 0 && (st.rx_speed + st.tx_speed) > config.alert_threshold;
            section.classList.toggle('alert', active);

            const chart = charts[iface];
            const samples = (data.history && data.history[iface]) || [];
            if (samples.length) {
                chart.data.labels = samples.map(() => '');
                chart.data.datasets[0].data = samples.map(s => s.rx);
                chart.data.datasets[1].data = samples.map(s => s.tx);
                chart.update();
                if (shouldPersist) {
                    saveHistoryToStorage(iface, samples);
                }
            }

            totalRxSpeed += st.rx_speed;
            totalTxSpeed += st.tx_speed;
        });

        document.getElementById('sum-rx-speed').innerText = formatSpeed(totalRxSpeed);
        document.getElementById('sum-tx-speed').innerText = formatSpeed(totalTxSpeed);
    } catch (e) {
        console.error("Poll error", e);
    }

    setTimeout(pollRealtime, config.poll_interval_ms || 1000);
}

function formatDateLabel(label) {
    if (currentRange === 'today') return label;
    const monthsEs = ['Ene', 'Feb', 'Mar', 'Abr', 'May', 'Jun', 'Jul', 'Ago', 'Sep', 'Oct', 'Nov', 'Dic'];
    const parts = label.split('-');
    if (parts.length < 3) return label;
    const monthName = monthsEs[parseInt(parts[1], 10) - 1] || parts[1];
    return `${monthName} ${parseInt(parts[2], 10)}, ${parts[0]}`;
}

async function loadHistory(range) {
    try {
        const res = await fetch('/api/history?range=' + encodeURIComponent(range));
        const data = await res.json();

        data.interfaces.forEach(ifaceData => {
            const historyDiv = document.getElementById(`history-${ifaceData.name}`);
            if (!historyDiv) return;

            const entries = [...ifaceData.entries].sort((a, b) => (b.rx + b.tx) - (a.rx + a.tx));

            if (!entries.length) {
                historyDiv.innerHTML = "<p style='text-align:center; color:#888; padding: 20px;'>No historical data yet.</p>";
                return;
            }

            let table = `<table>
                <tr>
                    <th><div class="table-header-cell"><span class="rx">${ICONS.download}</span> Fecha</div></th>
                    <th><div class="table-header-cell"><span class="rx">${ICONS.download}</span> Received</div></th>
                    <th><div class="table-header-cell"><span class="tx">${ICONS.upload}</span> Sent</div></th>
                    <th><div class="table-header-cell"><span class="total">${ICONS.wave}</span> Total</div></th>
                </tr>`;

            entries.forEach(e => {
                table += `
                    <tr>
                        <td>${formatDateLabel(e.label)}</td>
                        <td class="rx">${formatBytes(e.rx)}</td>
                        <td class="tx">${formatBytes(e.tx)}</td>
                        <td class="total font-bold">${formatBytes(e.rx + e.tx)}</td>
                    </tr>
                `;
            });
            table += `</table>`;
            historyDiv.innerHTML = table;
        });
    } catch (e) {
        console.error("History fetch error", e);
    }
}

async function refreshSummary() {
    try {
        const res = await fetch('/api/summary');
        const data = await res.json();
        document.getElementById('sum-rx-today').innerText = formatBytes(data.total_rx_today);
        document.getElementById('sum-tx-today').innerText = formatBytes(data.total_tx_today);
    } catch (e) {
        console.error("Summary fetch error", e);
    }
    setTimeout(refreshSummary, 5 * 60 * 1000);
}

function registerSW() {
    if ('serviceWorker' in navigator) {
        navigator.serviceWorker.register('./sw.js').catch(e => {
            console.warn('Service worker registration failed:', e);
        });
    }
}

window.onload = init;
