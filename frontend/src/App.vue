<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import {
  FetchInfo,
  CancelFetch,
  StartDownload,
  PauseDownload,
  CancelDownload,
  SelectDownloadFolder,
  CheckYtDlp,
  GetSettings,
  SaveSettings,
} from '../wailsjs/go/main/App'
import { EventsOn } from '../wailsjs/runtime/runtime'

const QUALITIES = [
  { value: 'best', label: 'Best available' },
  { value: '1080', label: '1080p (MP4)' },
  { value: '720', label: '720p (MP4)' },
  { value: '480', label: '480p (MP4)' },
  { value: '360', label: '360p (MP4)' },
  { value: 'audio', label: 'Audio only (MP3)' },
]

const MAX_PARALLEL_CHECKS = 3

const urlText = ref('')
const jobs = ref([])
const outputDir = ref('')
const defaultQuality = ref('best')
const maxConcurrent = ref(2)
const ytdlpVersion = ref('')
const ytdlpMissing = ref(false)
const notice = ref('')

let nextId = 1
const fetchQueue = []
let activeChecks = 0
let offProgress = null
let offStatus = null

// ---------- helpers ----------

const byId = (id) => jobs.value.find((j) => j.id === id)
const removeJob = (id) => { jobs.value = jobs.value.filter((j) => j.id !== id) }
const errText = (e) => (typeof e === 'string' ? e : e?.message || String(e))

function formatDuration(seconds) {
  if (!seconds) return ''
  const h = Math.floor(seconds / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  const s = Math.floor(seconds % 60)
  const ss = s.toString().padStart(2, '0')
  return h ? `${h}:${m.toString().padStart(2, '0')}:${ss}` : `${m}:${ss}`
}

// ---------- settings ----------

async function saveSettings() {
  try {
    await SaveSettings({
      downloadFolder: outputDir.value,
      maxConcurrent: maxConcurrent.value,
      defaultQuality: defaultQuality.value,
    })
  } catch (e) {
    // non-fatal, values still apply for this session
  }
}

async function pickFolder() {
  const dir = await SelectDownloadFolder()
  if (dir) {
    outputDir.value = dir
    notice.value = ''
    saveSettings()
  }
}

async function ensureFolder() {
  if (outputDir.value) return true
  notice.value = 'Choose a download folder first.'
  await pickFolder()
  return !!outputDir.value
}

function onMaxChange() {
  saveSettings()
  pump()
}

// ---------- adding links + checking them ----------

function addUrls() {
  const found = urlText.value
    .split(/\s+/)
    .map((s) => s.trim())
    .filter((s) => /^https?:\/\//i.test(s))

  if (!found.length) {
    notice.value = 'Paste at least one link that starts with http:// or https://'
    return
  }

  notice.value = ''
  let dupes = 0
  for (const url of found) {
    if (jobs.value.some((j) => j.url === url)) {
      dupes++
      continue
    }
    const job = {
      id: `j${Date.now()}-${nextId++}`,
      url,
      title: '',
      thumbnail: '',
      uploader: '',
      duration: 0,
      status: 'fetching',
      quality: defaultQuality.value,
      progress: 0,
      size: '',
      speed: '',
      eta: '',
      stage: 1,
      error: '',
      pausing: false,
      cancelling: false,
    }
    jobs.value.push(job)
    fetchQueue.push(job.id)
  }
  urlText.value = ''
  if (dupes) notice.value = `${dupes} link${dupes > 1 ? 's were' : ' was'} already in the list.`
  pumpFetch()
}

function pumpFetch() {
  while (activeChecks < MAX_PARALLEL_CHECKS && fetchQueue.length) {
    const id = fetchQueue.shift()
    const j = byId(id)
    if (!j || j.status !== 'fetching') continue // removed or cancelled while waiting
    activeChecks++
    runFetch(id)
  }
}

async function runFetch(id) {
  try {
    const info = await FetchInfo(id, byId(id).url)
    const j = byId(id)
    if (!j || j.status !== 'fetching') return
    j.title = info.title || j.url
    j.thumbnail = info.thumbnail || ''
    j.uploader = info.uploader || ''
    j.duration = info.duration || 0
    j.status = 'ready'
  } catch (e) {
    const j = byId(id)
    if (j && j.status === 'fetching') {
      j.status = 'error'
      j.error = errText(e)
    }
  } finally {
    activeChecks--
    pumpFetch()
  }
}

// ---------- queue + scheduler ----------

const activeCount = computed(
  () => jobs.value.filter((j) => j.status === 'downloading' || j.status === 'processing').length,
)
const readyCount = computed(() => jobs.value.filter((j) => j.status === 'ready').length)
const pausableCount = computed(
  () => jobs.value.filter((j) => j.status === 'downloading' || j.status === 'queued').length,
)
const pausedCount = computed(() => jobs.value.filter((j) => j.status === 'paused').length)
const doneCount = computed(() => jobs.value.filter((j) => j.status === 'done').length)

function pump() {
  for (const j of jobs.value) {
    if (activeCount.value >= maxConcurrent.value) return
    if (j.status === 'queued') startJob(j)
  }
}

async function startJob(j) {
  j.status = 'downloading'
  j.error = ''
  try {
    await StartDownload(j.id, j.url, j.quality, outputDir.value)
  } catch (e) {
    j.status = 'error'
    j.error = errText(e)
    pump()
  }
}

async function queueJob(j) {
  if (!(await ensureFolder())) return
  j.status = 'queued'
  j.error = ''
  pump()
}

async function downloadAll() {
  if (!readyCount.value) return
  if (!(await ensureFolder())) return
  for (const j of jobs.value) {
    if (j.status === 'ready') j.status = 'queued'
  }
  pump()
}

function pauseJob(j) {
  if (j.status !== 'downloading' || j.pausing) return
  j.pausing = true
  PauseDownload(j.id)
}

function pauseAll() {
  for (const j of jobs.value) {
    if (j.status === 'queued') j.status = 'paused' // hold it so it doesn't auto-start
    else if (j.status === 'downloading') pauseJob(j)
  }
}

function resumeJob(j) {
  j.status = 'queued'
  pump()
}

function resumeAll() {
  for (const j of jobs.value) {
    if (j.status === 'paused') j.status = 'queued'
  }
  pump()
}

function retryJob(j) {
  j.error = ''
  if (!j.title) {
    j.status = 'fetching'
    fetchQueue.push(j.id)
    pumpFetch()
  } else {
    queueJob(j)
  }
}

// Cancel/remove behaves differently depending on where the item is.
function cancelJob(j) {
  switch (j.status) {
    case 'fetching':
      CancelFetch(j.id)
      removeJob(j.id)
      break
    case 'queued':
      j.status = 'ready'
      break
    case 'downloading':
    case 'processing':
    case 'paused':
      j.cancelling = true
      CancelDownload(j.id) // deletes partial files, then a "cancelled" event removes the item
      break
    case 'error':
      if (j.title) {
        j.cancelling = true
        CancelDownload(j.id) // clean up anything the failed attempt left behind
      } else {
        removeJob(j.id)
      }
      break
    default: // ready, done
      removeJob(j.id)
  }
}

function cancelLabel(j) {
  if (j.cancelling) return 'Cancelling…'
  if (j.status === 'fetching' || j.status === 'downloading' || j.status === 'processing' || j.status === 'paused') return 'Cancel'
  if (j.status === 'queued') return 'Unqueue'
  return 'Remove'
}

function clearFinished() {
  jobs.value = jobs.value.filter((j) => j.status !== 'done')
}

// ---------- progress display ----------

const showProgress = (j) => ['queued', 'downloading', 'processing', 'paused'].includes(j.status)

function progressText(j) {
  switch (j.status) {
    case 'queued':
      return 'Waiting for a free slot…'
    case 'processing':
      return 'Processing…'
    case 'paused':
      return j.progress > 0 ? `Paused at ${j.progress.toFixed(1)}%` : 'Paused'
    case 'downloading': {
      const parts = [`${j.progress.toFixed(1)}%`]
      if (j.size) parts.push(`of ${j.size}`)
      if (j.speed) parts.push(`at ${j.speed}`)
      if (j.eta) parts.push(`ETA ${j.eta}`)
      if (j.stage > 1) parts.push(`(part ${j.stage})`)
      return parts.join(' ')
    }
  }
  return ''
}

// ---------- lifecycle ----------

onMounted(async () => {
  offProgress = EventsOn('job-progress', (d) => {
    const j = byId(d.id)
    if (!j || j.status !== 'downloading') return
    j.progress = Number(d.percent) || 0
    j.size = d.size || ''
    j.speed = d.speed || ''
    j.eta = d.eta || ''
    j.stage = d.stage || 1
  })

  offStatus = EventsOn('job-status', (d) => {
    const j = byId(d.id)
    if (!j) return
    switch (d.status) {
      case 'downloading':
        j.status = 'downloading'
        break
      case 'processing':
        j.status = 'processing'
        j.progress = 100
        j.speed = ''
        j.eta = ''
        break
      case 'paused':
        j.status = 'paused'
        j.pausing = false
        j.speed = ''
        j.eta = ''
        break
      case 'cancelled':
        removeJob(j.id)
        break
      case 'done':
        j.status = 'done'
        j.progress = 100
        j.speed = ''
        j.eta = ''
        break
      case 'error':
        j.status = 'error'
        j.error = d.error || 'Download failed'
        j.pausing = false
        j.cancelling = false
        break
    }
    pump() // a slot may have freed up
  })

  try {
    ytdlpVersion.value = await CheckYtDlp()
  } catch (e) {
    ytdlpMissing.value = true
  }

  try {
    const s = await GetSettings()
    if (s?.downloadFolder) outputDir.value = s.downloadFolder
    if (s?.maxConcurrent) maxConcurrent.value = s.maxConcurrent
    if (s?.defaultQuality) defaultQuality.value = s.defaultQuality
  } catch (e) {
    // first run, nothing saved yet
  }
})

onUnmounted(() => {
  if (offProgress) offProgress()
  if (offStatus) offStatus()
})
</script>

<template>
  <div class="app">
    <header>
      <h1>YT-DLP Downloader</h1>
      <span v-if="ytdlpVersion" class="version">yt-dlp {{ ytdlpVersion }}</span>
    </header>

    <div v-if="ytdlpMissing" class="banner error">
      yt-dlp was not found next to the app or on your PATH. Put <code>yt-dlp.exe</code> in the same folder as this app, or install it, then restart.
    </div>
    <div v-if="notice" class="banner info">{{ notice }}</div>

    <section class="card">
      <div class="settings-row">
        <span class="label">Download folder</span>
        <span class="folder" :title="outputDir">{{ outputDir || 'Not set' }}</span>
        <button class="secondary small" @click="pickFolder">{{ outputDir ? 'Change' : 'Choose' }}</button>
      </div>
      <div class="settings-row">
        <label class="label" for="dq">Default quality</label>
        <select id="dq" v-model="defaultQuality" @change="saveSettings">
          <option v-for="q in QUALITIES" :key="q.value" :value="q.value">{{ q.label }}</option>
        </select>
        <label class="label spaced" for="mc">Downloads at once</label>
        <select id="mc" v-model.number="maxConcurrent" @change="onMaxChange">
          <option v-for="n in 5" :key="n" :value="n">{{ n }}</option>
        </select>
      </div>
    </section>

    <section class="add">
      <textarea
        v-model="urlText"
        rows="2"
        placeholder="Paste one or more links, one per line. Press Enter to add."
        @keydown.enter.exact.prevent="addUrls"
      ></textarea>
      <button :disabled="!urlText.trim()" @click="addUrls">Add</button>
    </section>

    <div v-if="jobs.length" class="toolbar">
      <span class="count">
        {{ jobs.length }} item{{ jobs.length > 1 ? 's' : '' }}<template v-if="activeCount"> · {{ activeCount }} downloading</template>
      </span>
      <div class="spacer"></div>
      <button v-if="readyCount" @click="downloadAll">Download all ({{ readyCount }})</button>
      <button v-if="pausableCount" class="secondary" @click="pauseAll">Pause all</button>
      <button v-if="pausedCount" class="secondary" @click="resumeAll">Resume all</button>
      <button v-if="doneCount" class="ghost" @click="clearFinished">Clear finished</button>
    </div>

    <div v-if="!jobs.length" class="empty">Nothing here yet. Paste some links above to build your list.</div>

    <div v-for="j in jobs" :key="j.id" class="job" :class="j.status">
      <div class="thumb-wrap">
        <img v-if="j.thumbnail" :src="j.thumbnail" alt="" class="thumb" />
        <div v-else class="thumb placeholder"><span v-if="j.status === 'fetching'" class="spinner big"></span></div>
      </div>

      <div class="job-main">
        <div class="job-title" :title="j.title || j.url">{{ j.title || j.url }}</div>

        <div class="job-sub">
          <template v-if="j.status === 'fetching'"><span class="spinner"></span> Checking link…</template>
          <template v-else-if="j.status === 'done'">Finished</template>
          <template v-else>
            {{ j.uploader }}<span v-if="j.uploader && j.duration"> · </span>{{ formatDuration(j.duration) }}
          </template>
        </div>

        <div v-if="j.status === 'ready'" class="job-options">
          <select v-model="j.quality" aria-label="Quality">
            <option v-for="q in QUALITIES" :key="q.value" :value="q.value">{{ q.label }}</option>
          </select>
        </div>

        <div v-if="showProgress(j)" class="progress">
          <div class="bar">
            <div class="fill" :class="{ indeterminate: j.status === 'processing' }" :style="{ width: j.progress + '%' }"></div>
          </div>
          <div class="progress-text">{{ progressText(j) }}</div>
        </div>

        <div v-if="j.status === 'error'" class="job-error">{{ j.error }}</div>
      </div>

      <div class="job-actions">
        <button v-if="j.status === 'ready'" class="small" @click="queueJob(j)">Download</button>
        <button v-if="j.status === 'downloading'" class="secondary small" :disabled="j.pausing" @click="pauseJob(j)">
          {{ j.pausing ? 'Pausing…' : 'Pause' }}
        </button>
        <button v-if="j.status === 'paused'" class="small" @click="resumeJob(j)">Resume</button>
        <button v-if="j.status === 'error'" class="small" @click="retryJob(j)">Retry</button>
        <button class="ghost small" :disabled="j.cancelling" @click="cancelJob(j)">{{ cancelLabel(j) }}</button>
      </div>
    </div>
  </div>
</template>
