'use strict';
'require view';
'require dom';
'require form';
'require fs';
'require poll';
'require uci';
'require ui';
'require tools.widgets as widgets';

var STATUS_FILE = '/var/run/xbslinkd/status.json';

function fmtBytes(n) {
        if (n == null) return '-';
        if (n > 1073741824) return (n / 1073741824).toFixed(2) + ' GB';
        if (n > 1048576) return (n / 1048576).toFixed(2) + ' MB';
        if (n > 1024) return (n / 1024).toFixed(1) + ' KB';
        return String(n) + ' B';
}

function fmtUptime(sec) {
        if (!sec || sec < 0) return '-';
        var m = Math.floor(sec / 60);
        var s = sec % 60;
        return (m > 0 ? m + 'm ' : '') + s + 's';
}

return view.extend({
        handleRestart: function (ev) {
                return ui.changes.apply(true)
                        .catch(function () {})
                        .then(function () {
                                return fs.exec('/etc/init.d/xbslink', ['restart']);
                        })
                        .then(function () {
                                ui.addNotification(null, E('p', _('xbslinkd restarted')), 'info');
                        })
                        .catch(function (e) {
                                ui.addNotification(null, E('p', _('Restart failed: %s').format(e.message || e)), 'error');
                        });
        },

        load: function () {
                return Promise.all([uci.load('xbslink')]);
        },

        render: function () {
                var self = this;

                var summaryEl = E('div', { 'class': 'cbi-map-descr' }, _('Loading status…'));
                var tableEl = E('div');

                /* status poll (5s) */
                poll.add(L.bind(function () {
                        return fs.read(STATUS_FILE).then(function (data) {
                                var st = JSON.parse(data);
                                var nodes = st.nodes || [];
                                var stats = st.stats || {};
                                var cloud = st.cloud || {};

                                var cloudTxt = cloud.enabled
                                        ? (cloud.joined
                                                ? _('joined "%s"').format(cloud.name || '') +
                                                  (cloud.reachable ? ' ✓' : ' (' + _('port unreachable') + ')')
                                                : _('not joined'))
                                        : _('off');

                                summaryEl.textContent = _('Up %s · local node %s:%d · capture %s · cloud %s · captured %s frames / injected %s')
                                        .format(fmtUptime(st.uptime_sec), st.local_ip, st.local_port, st.interface,
                                                cloudTxt, String(stats.frames_captured || 0), String(stats.frames_injected || 0));

                                var rows = [];
                                for (var i = 0; i < nodes.length; i++) {
                                        var n = nodes[i];
                                        rows.push([
                                                n.ip + ':' + n.port + (n.connected ? '' : ' ⚠'),
                                                n.nickname || '-',
                                                n.version || '-',
                                                (n.ping_ms >= 0 ? n.ping_ms + ' ms' : '-'),
                                                String((n.devices || []).length),
                                                fmtBytes(n.rx_bytes) + ' / ' + n.rx_packets,
                                                fmtBytes(n.tx_bytes) + ' / ' + n.tx_packets
                                        ]);
                                }
                                if (!rows.length)
                                        rows.push([E('em', {}, _('No remote nodes connected')), '-', '-', '-', '-', '-', '-']);

                                var table = E('table', { 'class': 'table' }, [
                                        E('tr', { 'class': 'tr table-titles' }, [
                                                E('th', { 'class': 'th' }, _('Node')),
                                                E('th', { 'class': 'th' }, _('Nickname')),
                                                E('th', { 'class': 'th' }, _('Version')),
                                                E('th', { 'class': 'th' }, _('Ping')),
                                                E('th', { 'class': 'th' }, _('Devices')),
                                                E('th', { 'class': 'th' }, _('RX (bytes / pkts)')),
                                                E('th', { 'class': 'th' }, _('TX (bytes / pkts)'))
                                        ])
                                ]);
                                for (var r = 0; r < rows.length; r++) {
                                        var tr = E('tr', { 'class': 'tr' });
                                        for (var c = 0; c < rows[r].length; c++)
                                                tr.appendChild(E('td', { 'class': 'td' }, rows[r][c]));
                                        table.appendChild(tr);
                                }
                                dom.content(tableEl, table);
                        }).catch(function (e) {
                                summaryEl.textContent = (e && e.message)
                                        ? _('Cannot read %s: %s - check that xbslinkd is running').format(STATUS_FILE, e.message || e)
                                        : _('xbslinkd is not running or has not written a status file yet.');
                                dom.content(tableEl, E('em', {}, '-'));
                        });
                }, this), 5);

                /* ------------------------------ configuration form */
                var m, s, o;

                m = new form.Map('xbslink', _('XBSlink System Link Proxy'),
                        _('Go rewrite of XBSlink, wire-compatible with the original XBSlink clients. ' +
                          'Tunnels Xbox/PS3 system link LAN traffic between remote nodes over the internet.'));

                /* Chain an explicit service restart onto the stock "Save & Apply"
                 * flow: rpcd does not reliably run the init script reload for
                 * sideloaded packages on every build, so restart it ourselves
                 * after the UCI changes have been committed. */
                var stockApply = m.handleSaveApply;
                m.handleSaveApply = function (ev, mode) {
                        return stockApply.call(m, ev, mode).then(function () {
                                if (mode != 'save')
                                        return fs.exec('/etc/init.d/xbslink', ['restart']).then(function () {
                                                ui.addNotification(null,
                                                        E('p', _('Settings applied, xbslinkd restarted')), 'info');
                                        });
                        }).catch(function (e) {
                                ui.addNotification(null,
                                        E('p', _('Apply/restart failed: %s').format(e.message || e)), 'error');
                        });
                };

                s = m.section(form.NamedSection, '__status', '_status', _('Live Status'));
                s.render = L.bind(function (view, section_id) {
                        return E('div', {}, [summaryEl, tableEl]);
                }, this, m, '__status');

                s = m.section(form.TypedSection, 'xbslink', _('Basic Settings'));
                s.addremove = false;
                s.anonymous = true;

                o = s.option(form.Flag, 'enabled', _('Enabled'));
                o.rmempty = false;

                o = s.option(widgets.DeviceSelect, 'interface', _('Capture interface'),
                        _('LAN side to listen for console traffic. Select the LAN bridge - every bridge port is monitored automatically.'));
                o.noaliases = true;
                o.rmempty = false;

                o = s.option(form.Value, 'nickname', _('Nickname'), _('Shown to other XBSlink nodes'));
                o.placeholder = 'OpenWrt';
                o.rmempty = false;

                o = s.option(form.Value, 'port', _('Node port'),
                        _('UDP port for node-to-node traffic. Keep 31415 for original-client compatibility.'));
                o.datatype = 'port';
                o.placeholder = '31415';

                o = s.option(form.DynamicList, 'peers', _('Static peers'),
                        _('Remote nodes to announce to at startup, e.g. 1.2.3.4:31415 or a dyndns host. Nodes learn each other automatically afterwards.'));

                o = s.option(form.Flag, 'forward_high_port_broadcasts', _('Forward all high-port UDP broadcasts'),
                        _('Tunnel every UDP broadcast with destination port >= 1024 (needed by some PS3/PS4 games).'));

                o = s.option(form.Button, '__restart');
                o.title = _('Service control');
                o.inputtitle = _('Save & Apply, then restart xbslinkd');
                o.inputstyle = 'apply';
                o.onclick = ui.createHandlerFn(this, 'handleRestart');

                s = m.section(form.TypedSection, 'xbslink', _('Cloud node discovery'),
                        _('Discover remote nodes through a cloudlist server. The official xbslink.de server is offline - ' +
                          'point this at your own instance (the self-hosted PHP server ships with xbslinkd).'));
                s.addremove = false;
                s.anonymous = true;

                o = s.option(form.Flag, 'cloud_enabled', _('Use cloud'));
                o.rmempty = false;

                o = s.option(form.Value, 'cloud_server', _('Cloud server URL'));
                o.placeholder = 'http://10.165.0.105/cloudlist.php';
                o.depends('cloud_enabled', '1');

                o = s.option(form.Value, 'cloud_name', _('Cloud name'));
                o.depends('cloud_enabled', '1');
                o.rmempty = false;

                o = s.option(form.Value, 'cloud_password', _('Cloud password'),
                        _('Only checked when creating a new cloud.'));
                o.password = true;
                o.depends('cloud_enabled', '1');

                o = s.option(form.Value, 'cloud_max_nodes', _('Max nodes'));
                o.datatype = 'range(1,200)';
                o.placeholder = '10';
                o.depends('cloud_enabled', '1');

                o = s.option(form.Value, 'node_ip', _('Registered node IP'),
                        _('Leave empty for auto detection. Private IPs are replaced by the server with your public IP.'));
                o.datatype = 'ip4addr';
                o.depends('cloud_enabled', '1');

                s = m.section(form.TypedSection, 'xbslink', _('Advanced Settings'));
                s.addremove = false;
                s.anonymous = true;

                o = s.option(form.DynamicList, 'special_macs', _('Always tunnel these MACs'),
                        _('One MAC per line, press "+" to add more. Formats: aa:bb:cc:dd:ee:ff, ' +
                          'aa-bb-cc-dd-ee-ff or aabbccddeeff (the colon-less format of the Windows ' +
                          'XBSlink client works too). While this list is NOT empty, ONLY frames ' +
                          'from these devices are tunneled (original XBSlink "only forward special MACs" mode).'));
                o.placeholder = 'aa:bb:cc:dd:ee:ff';
                o.validate = function (section_id, value) {
                        if (value == null || value === '')
                                return true;
                        var v = String(value).replace(/[:.\-]/g, '');
                        if (!/^[0-9a-fA-F]{12}$/.test(v))
                                return _('Not a MAC address: expected aa:bb:cc:dd:ee:ff or aabbccddeeff');
                        return true;
                };

                o = s.option(form.Flag, 'allow_wellknown_ports', _('Allow ports below 1024'),
                        _('Do not exclude TCP/UDP destination ports < 1024 from the tunnel.'));

                o = s.option(form.Value, 'gateway_ips', _('Excluded gateway IPs'),
                        _('Comma separated. Automatically detected from the routing table when empty.'));
                o.datatype = 'list(ip4addr)';

                o = s.option(form.Value, 'status_port', _('Status HTTP port'),
                        _('Local-only JSON endpoint on 127.0.0.1, 0 disables it.'));
                o.datatype = 'portrange';
                o.placeholder = '31416';

                o = s.option(form.Flag, 'verbose', _('Verbose logging'),
                        _('Log every tunneled frame to the system log (debugging only!).'));

                return m.render();
        }
});
