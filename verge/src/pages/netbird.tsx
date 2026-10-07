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
  pixiuConnect,
  setPixiuWifiBlacklist,
  type NetBirdDaemon,
} from '@/services/cmds'

const NetBirdPage = () => {
  const [daemon, setDaemon] = useState<NetBirdDaemon | null>(null)
  const [blacklist, setBlacklist] = useState('')
  const [warning, setWarning] = useState('')
  const [error, setError] = useState('')

  const refresh = useCallback(async () => {
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
    void refresh()
  }, [refresh])

  const saveList = async () => {
    await setPixiuWifiBlacklist(blacklist)
    await refresh()
  }

  const connect = async (confirm: boolean) => {
    const result = await pixiuConnect(confirm)
    if (result.warning && !confirm) {
      setWarning(result.warning)
      return
    }
    setWarning('')
    await refresh()
  }

  return (
    <BasePage title="NetBird">
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2, p: 2 }}>
        {error ? <Alert severity="error">{error}</Alert> : null}
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
          <HubOutlined fontSize="small" />
          <Typography>
            {daemon?.running
              ? `Daemon ${daemon.status || 'up'}${daemon.interfaceName ? ` on ${daemon.interfaceName}` : ''}`
              : daemon?.message || 'netbird daemon is not running'}
          </Typography>
          <Button onClick={() => void refresh()}>Refresh</Button>
          <Button variant="contained" onClick={() => void connect(false)}>
            Connect
          </Button>
        </Box>
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
