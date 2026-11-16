import {
  Alert,
  Box,
  Button,
  Chip,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  MenuItem,
  Stack,
  Tab,
  Tabs,
  TextField,
  Typography,
} from '@mui/material'
import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { BasePage } from '@/components/base'
import {
  getNetBirdDaemon,
  getPixiuWifiBlacklist,
  pixiuApplySrvProfile,
  pixiuConnect,
  pixiuDisconnect,
  pixiuSetNetworks,
  pixiuSwitchProfile,
  setPixiuWifiBlacklist,
  type NetBirdDaemon,
  type NetBirdNetwork,
  type NetBirdPeer,
  type PixiuSrvApply,
} from '@/services/cmds'
import { openExternalUrl } from '@/utils/open-external-url'

type PeerFilter = 'all' | 'online' | 'offline'
type Section = 'peers' | 'networks'

const STATUS_KEYS = {
  Connected: 'netbird.status.connected',
  Connecting: 'netbird.status.connecting',
  Idle: 'netbird.status.idle',
  NeedsLogin: 'netbird.status.needsLogin',
  LoginFailed: 'netbird.status.loginFailed',
  SessionExpired: 'netbird.status.sessionExpired',
} as const

function netbirdConnected(daemon: NetBirdDaemon | null) {
  return daemon?.status.toLowerCase() === 'connected'
}

function isExitNode(range: string) {
  return range.split(',').some((part) => {
    const trimmed = part.trim()
    return trimmed === '0.0.0.0/0' || trimmed === '::/0'
  })
}

function managementHost(url: string) {
  try {
    return new URL(url).hostname
  } catch {
    return ''
  }
}

function ssidFromWarning(warning: string) {
  return warning.match(/Wi-Fi "([^"]*)"/)?.[1] ?? ''
}

function formatBytes(value: number) {
  if (!Number.isFinite(value) || value <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB']
  let size = value
  let index = 0
  while (size >= 1024 && index < units.length - 1) {
    size /= 1024
    index += 1
  }
  const digits = size >= 10 || index === 0 ? 0 : 1
  return `${size.toFixed(digits)} ${units[index]}`
}

function peerOnline(peer: NetBirdPeer) {
  return peer.connStatus === 'Connected'
}

function Panel({ children }: { children: ReactNode }) {
  return (
    <Box
      sx={{
        border: 1,
        borderColor: 'divider',
        borderRadius: 2,
        p: 2,
        bgcolor: 'background.paper',
      }}
    >
      {children}
    </Box>
  )
}

const NetBirdPage = () => {
  const { t } = useTranslation()
  const [daemon, setDaemon] = useState<NetBirdDaemon | null>(null)
  const [blacklist, setBlacklist] = useState('')
  const [warning, setWarning] = useState('')
  const [error, setError] = useState('')
  const [applying, setApplying] = useState(false)
  const [acting, setActing] = useState(false)
  const [srv, setSrv] = useState<PixiuSrvApply | null>(null)
  const [section, setSection] = useState<Section>('peers')
  const [peerQuery, setPeerQuery] = useState('')
  const [peerFilter, setPeerFilter] = useState<PeerFilter>('all')
  const [selectedKey, setSelectedKey] = useState('')
  const [networkQuery, setNetworkQuery] = useState('')

  const load = useCallback(async () => {
    try {
      const [status, list] = await Promise.all([
        getNetBirdDaemon(),
        getPixiuWifiBlacklist(),
      ])
      setDaemon(status)
      setBlacklist(list)
      setError('')
    } catch (err) {
      setError(err instanceof Error ? err.message : 'netbird.errors.read')
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  const connected = netbirdConnected(daemon)
  const peers = daemon?.peers ?? []
  const routes = daemon?.networks ?? []
  const exitNodes = routes.filter((route) => isExitNode(route.range))
  const networks = routes.filter((route) => !isExitNode(route.range))
  const activeExit = exitNodes.find((route) => route.selected)

  const statusLabel = daemon?.status
    ? t(STATUS_KEYS[daemon.status as keyof typeof STATUS_KEYS] ?? 'netbird.status.unknown')
    : t('netbird.status.daemonDown')

  const filteredPeers = useMemo(() => {
    const query = peerQuery.trim().toLowerCase()
    return [...peers]
      .sort((left, right) => {
        const online = Number(peerOnline(right)) - Number(peerOnline(left))
        if (online !== 0) return online
        return (left.fqdn || left.ip).localeCompare(right.fqdn || right.ip)
      })
      .filter((peer) => {
        if (peerFilter === 'online' && !peerOnline(peer)) return false
        if (peerFilter === 'offline' && peerOnline(peer)) return false
        if (!query) return true
        return (
          peer.fqdn.toLowerCase().includes(query) ||
          peer.ip.includes(query) ||
          peer.ipv6.includes(query)
        )
      })
  }, [peerFilter, peerQuery, peers])

  const filteredNetworks = useMemo(() => {
    const query = networkQuery.trim().toLowerCase()
    return networks.filter((route) => {
      if (!query) return true
      return [route.id, route.range, ...route.domains]
        .join(' ')
        .toLowerCase()
        .includes(query)
    })
  }, [networkQuery, networks])

  const saveList = async () => {
    await setPixiuWifiBlacklist(blacklist)
    await load()
  }

  const connect = async (confirm: boolean) => {
    setActing(true)
    try {
      const result = await pixiuConnect(confirm)
      if (result.warning && !confirm) {
        setWarning(result.warning)
        return
      }
      setWarning('')
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'netbird.errors.action')
    } finally {
      setActing(false)
    }
  }

  const disconnect = async () => {
    setActing(true)
    try {
      await pixiuDisconnect()
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'netbird.errors.action')
    } finally {
      setActing(false)
    }
  }

  const applySrv = async () => {
    setApplying(true)
    setSrv(null)
    try {
      setSrv(await pixiuApplySrvProfile())
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'netbird.errors.lookup')
    } finally {
      setApplying(false)
    }
  }

  const switchProfile = async (profile: string) => {
    setActing(true)
    try {
      await pixiuSwitchProfile(profile)
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'netbird.errors.action')
    } finally {
      setActing(false)
    }
  }

  const setRoute = async (ids: string[], selected: boolean, all = false) => {
    setActing(true)
    try {
      await pixiuSetNetworks(ids, selected, all)
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'netbird.errors.action')
    } finally {
      setActing(false)
    }
  }

  const chooseExit = async (id: string) => {
    if (!id) {
      if (activeExit) await setRoute([activeExit.id], false)
      return
    }
    await setRoute([id], true)
  }

  const host =
    srv?.host || managementHost(daemon?.managementUrl ?? '') || t('netbird.srv.thisServer')
  const srvMessage = !srv
    ? ''
    : srv.applied && srv.current
      ? t('netbird.srv.saved', { url: srv.url, host })
      : srv.applied
        ? t('netbird.srv.savedNotCurrent', { url: srv.url, host })
        : srv.kind === 'missing'
          ? t('netbird.srv.missing', { host })
          : srv.kind === 'rejected'
            ? t('netbird.srv.rejected', { host })
            : srv.kind === 'transient'
              ? t('netbird.srv.transient')
              : t('netbird.srv.unchanged')

  const selectedPeer = peers.find((peer) => peer.pubKey === selectedKey) ?? null
  const allNetworksSelected =
    filteredNetworks.length > 0 && filteredNetworks.every((route) => route.selected)

  return (
    <BasePage title={t('netbird.page.title')}>
      <Stack spacing={1.5} sx={{ pb: 2 }}>
        {error ? (
          <Alert severity="error">
            {error === 'netbird.errors.read'
              ? t('netbird.errors.read')
              : error === 'netbird.errors.action'
                ? t('netbird.errors.action')
                : error === 'netbird.errors.lookup'
                  ? t('netbird.errors.lookup')
                  : error}
          </Alert>
        ) : null}
        <Box
          sx={{
            display: 'grid',
            gap: 1.5,
            gridTemplateColumns: { xs: '1fr', lg: '340px minmax(0, 1fr)' },
            alignItems: 'start',
          }}
        >
          <Stack spacing={1.5}>
            <Panel>
              <Stack spacing={1.25}>
                <Stack direction="row" spacing={1} sx={{ alignItems: 'center' }}>
                  <Chip
                    size="small"
                    color={connected ? 'success' : daemon?.running ? 'warning' : 'default'}
                    label={daemon?.running ? statusLabel : t('netbird.status.daemonDown')}
                  />
                  {daemon?.daemonVersion ? (
                    <Typography variant="body2" color="text.secondary">
                      {t('netbird.status.version', { version: daemon.daemonVersion })}
                    </Typography>
                  ) : null}
                </Stack>
                <Typography variant="body2">
                  {daemon?.local.fqdn || daemon?.local.ip
                    ? `${daemon.local.fqdn || t('netbird.status.localPeer')} · ${daemon.local.ip}${daemon.local.ipv6 ? ` · ${daemon.local.ipv6}` : ''}`
                    : t('netbird.status.localPeer')}
                </Typography>
                <LinkLine
                  label={t('netbird.status.management')}
                  link={daemon?.management}
                  online={t('netbird.status.online')}
                  offline={t('netbird.status.offline')}
                />
                <LinkLine
                  label={t('netbird.status.signal')}
                  link={daemon?.signal}
                  online={t('netbird.status.online')}
                  offline={t('netbird.status.offline')}
                />
              </Stack>
            </Panel>
            <Panel>
              <Stack spacing={1.5}>
                {!daemon?.disableProfiles && (daemon?.profiles.length ?? 0) > 0 ? (
                  <TextField
                    select
                    size="small"
                    label={t('netbird.profiles.label')}
                    value={daemon?.profiles.find((profile) => profile.active)?.id || ''}
                    disabled={acting}
                    onChange={(event) => void switchProfile(event.target.value)}
                  >
                    {daemon?.profiles.map((profile) => (
                      <MenuItem key={profile.id || profile.name} value={profile.id || profile.name}>
                        {profile.name || profile.id}
                      </MenuItem>
                    ))}
                  </TextField>
                ) : null}
                {!daemon?.disableNetworks ? (
                  <TextField
                    select
                    size="small"
                    label={t('netbird.exitNodes.label')}
                    value={activeExit?.id ?? ''}
                    disabled={acting || !connected || exitNodes.length === 0}
                    helperText={exitNodes.length === 0 ? t('netbird.exitNodes.empty') : undefined}
                    onChange={(event) => void chooseExit(event.target.value)}
                  >
                    <MenuItem value="">{t('netbird.exitNodes.none')}</MenuItem>
                    {exitNodes.map((route) => (
                      <MenuItem key={route.id} value={route.id}>
                        {route.id}
                      </MenuItem>
                    ))}
                  </TextField>
                ) : null}
                <Stack direction="row" spacing={1} useFlexGap sx={{ flexWrap: 'wrap' }}>
                  {connected ? (
                    <Button variant="contained" disabled={acting} onClick={() => void disconnect()}>
                      {t('netbird.actions.disconnect')}
                    </Button>
                  ) : (
                    <Button
                      variant="contained"
                      disabled={acting}
                      onClick={() => void connect(false)}
                    >
                      {t('netbird.actions.connect')}
                    </Button>
                  )}
                  <Button variant="outlined" disabled={applying} onClick={() => void applySrv()}>
                    {applying
                      ? t('netbird.actions.lookingUp')
                      : t('netbird.actions.useServerProfile')}
                  </Button>
                  {daemon?.adminUrl ? (
                    <Button variant="text" onClick={() => void openExternalUrl(daemon.adminUrl)}>
                      {t('netbird.actions.openAdmin')}
                    </Button>
                  ) : null}
                </Stack>
                {srvMessage ? (
                  <Alert severity={srv?.applied ? 'success' : srv?.kind === 'transient' ? 'error' : 'warning'}>
                    {srvMessage}
                  </Alert>
                ) : null}
                <TextField
                  label={t('netbird.blacklist.label')}
                  helperText={t('netbird.blacklist.helper')}
                  value={blacklist}
                  onChange={(event) => setBlacklist(event.target.value)}
                  onBlur={() => void saveList()}
                  multiline
                  minRows={3}
                  fullWidth
                  size="small"
                />
              </Stack>
            </Panel>
          </Stack>
          <Panel>
            <Tabs
              value={daemon?.disableNetworks && section === 'networks' ? 'peers' : section}
              onChange={(_, value: Section) => setSection(value)}
              sx={{ minHeight: 40, mb: 1.5 }}
            >
              <Tab value="peers" label={t('netbird.peers.title')} sx={{ minHeight: 40 }} />
              {daemon?.disableNetworks ? null : (
                <Tab value="networks" label={t('netbird.networks.title')} sx={{ minHeight: 40 }} />
              )}
            </Tabs>
            {!connected ? (
              <Typography color="text.secondary" sx={{ py: 6, textAlign: 'center' }}>
                {section === 'networks'
                  ? t('netbird.networks.notConnected')
                  : t('netbird.peers.notConnected')}
              </Typography>
            ) : section === 'networks' && !daemon?.disableNetworks ? (
              <NetworkList
                routes={filteredNetworks}
                query={networkQuery}
                onQuery={setNetworkQuery}
                acting={acting}
                allSelected={allNetworksSelected}
                onToggle={(route) => void setRoute([route.id], !route.selected)}
                onBulk={() =>
                  void setRoute(
                    filteredNetworks.map((route) => route.id),
                    !allNetworksSelected,
                  )
                }
              />
            ) : (
              <PeerList
                peers={filteredPeers}
                total={peers.length}
                query={peerQuery}
                filter={peerFilter}
                selected={selectedPeer}
                onQuery={setPeerQuery}
                onFilter={setPeerFilter}
                onSelect={(peer) =>
                  setSelectedKey((current) => (current === peer.pubKey ? '' : peer.pubKey))
                }
              />
            )}
          </Panel>
        </Box>
      </Stack>
      <Dialog open={warning.length > 0} onClose={() => setWarning('')}>
        <DialogTitle>{t('netbird.blacklist.dialogTitle')}</DialogTitle>
        <DialogContent>
          <Typography>
            {t('netbird.blacklist.warning', { ssid: ssidFromWarning(warning) })}
          </Typography>
        </DialogContent>
        <DialogActions>
          <Button autoFocus onClick={() => setWarning('')}>
            {t('netbird.blacklist.no')}
          </Button>
          <Button
            onClick={() => {
              const pending = warning
              setWarning('')
              void connect(true).catch(() => setWarning(pending))
            }}
          >
            {t('netbird.blacklist.yes')}
          </Button>
        </DialogActions>
      </Dialog>
    </BasePage>
  )
}

function LinkLine({
  label,
  link,
  online,
  offline,
}: {
  label: string
  link?: { url: string; connected: boolean; error: string }
  online: string
  offline: string
}) {
  if (!link?.url) return null
  return (
    <Typography variant="body2" color="text.secondary" sx={{ wordBreak: 'break-all' }}>
      {label}: {link.connected ? online : offline}
      {link.url ? ` · ${link.url}` : ''}
      {link.error ? ` · ${link.error}` : ''}
    </Typography>
  )
}

function PeerList({
  peers,
  total,
  query,
  filter,
  selected,
  onQuery,
  onFilter,
  onSelect,
}: {
  peers: NetBirdPeer[]
  total: number
  query: string
  filter: PeerFilter
  selected: NetBirdPeer | null
  onQuery: (value: string) => void
  onFilter: (value: PeerFilter) => void
  onSelect: (peer: NetBirdPeer) => void
}) {
  const { t } = useTranslation()
  return (
    <Stack spacing={1}>
      <Stack direction="row" spacing={1} useFlexGap sx={{ flexWrap: 'wrap' }}>
        <TextField
          size="small"
          value={query}
          placeholder={t('netbird.peers.search')}
          onChange={(event) => onQuery(event.target.value)}
          sx={{ flex: 1, minWidth: 160 }}
        />
        {(
          [
            ['all', 'netbird.peers.all'],
            ['online', 'netbird.peers.online'],
            ['offline', 'netbird.peers.offline'],
          ] as const
        ).map(([value, key]) => (
          <Chip
            key={value}
            label={t(key)}
            color={filter === value ? 'primary' : 'default'}
            onClick={() => onFilter(value)}
          />
        ))}
      </Stack>
      {total === 0 ? (
        <Empty title={t('netbird.peers.empty')} hint={t('netbird.peers.emptyHint')} />
      ) : peers.length === 0 ? (
        <Empty title={t('netbird.peers.noResults')} hint="" />
      ) : (
        peers.map((peer) => {
          const open = selected?.pubKey === peer.pubKey
          return (
            <Box key={peer.pubKey || peer.ip} sx={{ borderBottom: 1, borderColor: 'divider' }}>
              <Button
                fullWidth
                onClick={() => onSelect(peer)}
                sx={{ justifyContent: 'space-between', textTransform: 'none', px: 0.5 }}
              >
                <Stack direction="row" spacing={1} sx={{ alignItems: 'center', minWidth: 0 }}>
                  <Box
                    sx={{
                      width: 8,
                      height: 8,
                      borderRadius: '50%',
                      bgcolor: peerOnline(peer) ? 'success.main' : 'text.disabled',
                      flex: 'none',
                    }}
                  />
                  <Typography noWrap>
                    {peer.fqdn || peer.ip || t('netbird.peers.title')}
                  </Typography>
                </Stack>
                <Typography variant="body2" color="text.secondary" sx={{ flex: 'none' }}>
                  {peer.ip}
                  {peer.latencyMs > 0 ? ` · ${peer.latencyMs} ms` : ''}
                </Typography>
              </Button>
              {open ? (
                <Stack spacing={0.5} sx={{ pb: 1.5, pl: 2 }}>
                  <Detail label={t('netbird.peers.key')} value={peer.pubKey} />
                  <Detail
                    label={t('netbird.peers.received')}
                    value={formatBytes(peer.bytesRx)}
                  />
                  <Detail label={t('netbird.peers.sent')} value={formatBytes(peer.bytesTx)} />
                  <Detail
                    label={t('netbird.peers.path')}
                    value={peer.relayed ? t('netbird.peers.relayed') : t('netbird.peers.direct')}
                  />
                  <Detail label={t('netbird.peers.endpoint')} value={peer.remoteEndpoint} />
                  <Detail label={t('netbird.peers.networks')} value={peer.networks.join(', ')} />
                </Stack>
              ) : null}
            </Box>
          )
        })
      )}
    </Stack>
  )
}

function NetworkList({
  routes,
  query,
  onQuery,
  acting,
  allSelected,
  onToggle,
  onBulk,
}: {
  routes: NetBirdNetwork[]
  query: string
  onQuery: (value: string) => void
  acting: boolean
  allSelected: boolean
  onToggle: (route: NetBirdNetwork) => void
  onBulk: () => void
}) {
  const { t } = useTranslation()
  return (
    <Stack spacing={1}>
      <Stack direction="row" spacing={1}>
        <TextField
          size="small"
          value={query}
          placeholder={t('netbird.networks.search')}
          onChange={(event) => onQuery(event.target.value)}
          sx={{ flex: 1 }}
        />
        <Button size="small" disabled={acting || routes.length === 0} onClick={onBulk}>
          {allSelected ? t('netbird.networks.disableAll') : t('netbird.networks.enableAll')}
        </Button>
      </Stack>
      {routes.length === 0 ? (
        <Empty
          title={query ? t('netbird.networks.noResults') : t('netbird.networks.empty')}
          hint={query ? '' : t('netbird.networks.emptyHint')}
        />
      ) : (
        routes.map((route) => (
          <Stack
            key={route.id}
            direction="row"
            spacing={1}
            sx={{ alignItems: 'center', justifyContent: 'space-between', py: 0.5 }}
          >
            <Box sx={{ minWidth: 0 }}>
              <Typography noWrap>{route.id}</Typography>
              <Typography variant="body2" color="text.secondary" noWrap>
                {route.domains.length > 0 ? route.domains.join(', ') : route.range}
              </Typography>
            </Box>
            <Chip
              label={route.selected ? t('netbird.networks.selected') : t('netbird.networks.off')}
              color={route.selected ? 'success' : 'default'}
              onClick={() => onToggle(route)}
              disabled={acting}
            />
          </Stack>
        ))
      )}
    </Stack>
  )
}

function Detail({ label, value }: { label: string; value: string }) {
  if (!value) return null
  return (
    <Typography variant="body2" sx={{ wordBreak: 'break-all' }}>
      <Typography component="span" variant="body2" color="text.secondary">
        {label}:{' '}
      </Typography>
      {value}
    </Typography>
  )
}

function Empty({ title, hint }: { title: string; hint: string }) {
  return (
    <Box sx={{ py: 6, textAlign: 'center' }}>
      <Typography>{title}</Typography>
      {hint ? (
        <Typography variant="body2" color="text.secondary">
          {hint}
        </Typography>
      ) : null}
    </Box>
  )
}

export default NetBirdPage
