import { HubOutlined } from '@mui/icons-material'
import {
  Alert,
  Box,
  Button,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  TextField,
  Typography,
} from '@mui/material'
import { useCallback, useEffect, useState } from 'react'

import { BasePage } from '@/components/base'
import {
  getNetBirdDaemon,
  getPixiuWifiBlacklist,
  pixiuApplySrvProfile,
  pixiuConnect,
  setPixiuWifiBlacklist,
  type NetBirdDaemon,
  type PixiuSrvApply,
} from '@/services/cmds'

function netbirdConnected(daemon: NetBirdDaemon | null) {
  return daemon?.status.toLowerCase() === 'connected'
}

function srvSeverity(result: PixiuSrvApply): 'success' | 'warning' | 'error' {
  if (result.applied) return 'success'
  if (result.kind === 'transient') return 'error'
  return 'warning'
}

const NetBirdPage = () => {
  const [daemon, setDaemon] = useState<NetBirdDaemon | null>(null)
  const [blacklist, setBlacklist] = useState('')
  const [warning, setWarning] = useState('')
  const [error, setError] = useState('')
  const [applying, setApplying] = useState(false)
  const [srv, setSrv] = useState<PixiuSrvApply | null>(null)

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
      setError(err instanceof Error ? err.message : 'Could not read NetBird')
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  const saveList = async () => {
    await setPixiuWifiBlacklist(blacklist)
    await load()
  }

  const connect = async (confirm: boolean) => {
    const result = await pixiuConnect(confirm)
    if (result.warning && !confirm) {
      setWarning(result.warning)
      return
    }
    setWarning('')
    await load()
  }

  const applySrv = async () => {
    setApplying(true)
    setSrv(null)
    try {
      setSrv(await pixiuApplySrvProfile())
    } catch (err) {
      setSrv({
        applied: false,
        kind: 'transient',
        message:
          err instanceof Error
            ? err.message
            : 'Could not look up the server profile',
        url: '',
      })
    } finally {
      setApplying(false)
    }
  }

  const connected = netbirdConnected(daemon)

  return (
    <BasePage title="NetBird">
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2, p: 2 }}>
        {error ? <Alert severity="error">{error}</Alert> : null}
        <Box
          sx={{
            display: 'flex',
            alignItems: 'center',
            gap: 1,
            flexWrap: 'wrap',
          }}
        >
          <HubOutlined fontSize="small" />
          <Typography sx={{ flex: 1, minWidth: 180 }}>
            {daemon?.running
              ? `Daemon ${daemon.status || 'up'}${daemon.interfaceName ? ` on ${daemon.interfaceName}` : ''}`
              : daemon?.message || 'netbird daemon is not running'}
          </Typography>
          {connected ? null : (
            <Button variant="contained" onClick={() => void connect(false)}>
              Connect
            </Button>
          )}
        </Box>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, flexWrap: 'wrap' }}>
          <Button
            variant="outlined"
            disabled={applying}
            onClick={() => void applySrv()}
          >
            {applying ? 'Looking up server profile…' : 'Use server profile'}
          </Button>
        </Box>
        {srv ? <Alert severity={srvSeverity(srv)}>{srv.message}</Alert> : null}
        {daemon?.adminUrl ? (
          <Box
            component="iframe"
            title="NetBird admin"
            src={daemon.adminUrl}
            sx={{ width: '100%', minHeight: 480, border: 0, borderRadius: 1 }}
          />
        ) : (
          <Alert severity="info">
            The admin URL appears here when the local daemon is running.
          </Alert>
        )}
        <TextField
          label="Wi-Fi blacklist"
          helperText="One SSID per line. An empty list, another network, or a failed scan does not block."
          value={blacklist}
          onChange={(event) => setBlacklist(event.target.value)}
          onBlur={() => void saveList()}
          multiline
          minRows={4}
          fullWidth
        />
      </Box>
      <Dialog open={warning.length > 0} onClose={() => setWarning('')}>
        <DialogTitle>Wi-Fi blacklist</DialogTitle>
        <DialogContent>
          <Typography>{warning}</Typography>
        </DialogContent>
        <DialogActions>
          <Button autoFocus onClick={() => setWarning('')}>
            No
          </Button>
          <Button
            onClick={() => {
              const pending = warning
              setWarning('')
              void connect(true).catch(() => setWarning(pending))
            }}
          >
            Yes
          </Button>
        </DialogActions>
      </Dialog>
    </BasePage>
  )
}

export default NetBirdPage
