// SkolkoZaAvto Front-end Logic

document.addEventListener('DOMContentLoaded', () => {
  // Elements
  const formSection = document.getElementById('form-section');
  const uploadingSection = document.getElementById('uploading-section');
  const waitingSection = document.getElementById('waiting-section');
  const resultSection = document.getElementById('result-section');

  const photoZone = document.getElementById('photo-zone');
  const photoInput = document.getElementById('photo-input');
  const photoPreviews = document.getElementById('photo-previews');

  const videoZone = document.getElementById('video-zone');
  const videoInput = document.getElementById('video-input');
  const videoPreviews = document.getElementById('video-previews');

  const phoneInput = document.getElementById('phone-input');
  const submitBtn = document.getElementById('submit-btn');

  const progressBar = document.getElementById('progress-bar');
  const progressPercent = document.getElementById('progress-percent');
  const uploadStatusText = document.getElementById('upload-status-text');

  const timerSeconds = document.getElementById('timer-seconds');
  const requestIdDisplay = document.getElementById('request-id-display');

  const priceRangeDisplay = document.getElementById('price-range-display');
  const specialistCommentDisplay = document.getElementById('specialist-comment-display');
  const resetBtn = document.getElementById('reset-btn');

  // Local State
  let photoFiles = [];
  let videoFile = null;
  let pollInterval = null;
  let timerInterval = null;
  let uploadStartTime = 0;

  // Base API configuration (handles local dev and native wraps automatically)
  const API_BASE = window.location.origin.includes('localhost') || window.location.origin.startsWith('file://')
    ? window.location.origin
    : window.location.origin;

  // Check for active saved session
  checkSavedSession();

  // === FILE HANDLING ===

  // Drag over effects
  ['dragenter', 'dragover'].forEach(eventName => {
    photoZone.addEventListener(eventName, (e) => {
      e.preventDefault();
      photoZone.classList.add('dragover');
    }, false);
    
    videoZone.addEventListener(eventName, (e) => {
      e.preventDefault();
      videoZone.classList.add('dragover');
    }, false);
  });

  ['dragleave', 'drop'].forEach(eventName => {
    photoZone.addEventListener(eventName, (e) => {
      e.preventDefault();
      photoZone.classList.remove('dragover');
    }, false);
    
    videoZone.addEventListener(eventName, (e) => {
      e.preventDefault();
      videoZone.classList.remove('dragover');
    }, false);
  });

  // Handle drops
  photoZone.addEventListener('drop', (e) => {
    const dt = e.dataTransfer;
    const files = Array.from(dt.files).filter(file => file.type.startsWith('image/'));
    addPhotos(files);
  });

  videoZone.addEventListener('drop', (e) => {
    const dt = e.dataTransfer;
    const files = Array.from(dt.files).filter(file => file.type.startsWith('video/'));
    if (files.length > 0) {
      setVideo(files[0]);
    }
  });

  // Handle clicks / input change
  photoInput.addEventListener('change', (e) => {
    const files = Array.from(e.target.files);
    addPhotos(files);
  });

  videoInput.addEventListener('change', (e) => {
    if (e.target.files.length > 0) {
      setVideo(e.target.files[0]);
    }
  });

  function addPhotos(files) {
    // Limit to 10 photos max
    const newPhotos = files.slice(0, 10 - photoFiles.length);
    photoFiles = [...photoFiles, ...newPhotos];
    renderPreviews();
    validateForm();
  }

  function setVideo(file) {
    // Limit size to 100MB roughly
    if (file.size > 100 * 1024 * 1024) {
      alert("Файл видео слишком большой. Пожалуйста, загрузите видео меньше 100 МБ.");
      return;
    }
    videoFile = file;
    renderPreviews();
    validateForm();
  }

  function removePhoto(index) {
    photoFiles.splice(index, 1);
    renderPreviews();
    validateForm();
  }

  function removeVideo() {
    videoFile = null;
    renderPreviews();
    validateForm();
  }

  function renderPreviews() {
    // Clear
    photoPreviews.innerHTML = '';
    videoPreviews.innerHTML = '';

    // Photos
    photoFiles.forEach((file, index) => {
      const item = document.createElement('div');
      item.className = 'preview-item';
      
      const img = document.createElement('img');
      img.src = URL.createObjectURL(file);
      img.onload = () => URL.revokeObjectURL(img.src);
      
      const remove = document.createElement('button');
      remove.className = 'preview-remove';
      remove.innerHTML = '×';
      remove.onclick = (e) => {
        e.stopPropagation();
        removePhoto(index);
      };

      item.appendChild(img);
      item.appendChild(remove);
      photoPreviews.appendChild(item);
    });

    // Video
    if (videoFile) {
      const item = document.createElement('div');
      item.className = 'preview-item';
      
      const video = document.createElement('video');
      video.src = URL.createObjectURL(videoFile);
      video.muted = true;
      video.currentTime = 1; // Show first frame
      video.onloadeddata = () => URL.revokeObjectURL(video.src);

      const badge = document.createElement('div');
      badge.className = 'video-badge';
      badge.innerText = 'ВИДЕО';
      
      const remove = document.createElement('button');
      remove.className = 'preview-remove';
      remove.innerHTML = '×';
      remove.onclick = (e) => {
        e.stopPropagation();
        removeVideo();
      };

      item.appendChild(video);
      item.appendChild(badge);
      item.appendChild(remove);
      videoPreviews.appendChild(item);
    }
  }

  phoneInput.addEventListener('input', validateForm);

  function validateForm() {
    const hasMedia = photoFiles.length > 0 || videoFile !== null;
    const hasPhone = phoneInput.value.trim().length >= 2;
    submitBtn.disabled = !(hasMedia && hasPhone);
  }

  // === SUBMISSION & PROGRESS ===

  submitBtn.addEventListener('click', () => {
    if (submitBtn.disabled) return;

    // Transition to Uploading Screen
    formSection.classList.add('hidden');
    uploadingSection.classList.remove('hidden');

    const formData = new FormData();
    formData.append('phone', phoneInput.value.trim());
    
    photoFiles.forEach((file) => {
      formData.append('photos', file);
    });

    if (videoFile) {
      formData.append('video', videoFile);
    }

    const xhr = new XMLHttpRequest();
    xhr.open('POST', `${API_BASE}/api/upload`, true);

    // Track upload progress
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable) {
        const percentComplete = Math.round((e.loaded / e.total) * 100);
        progressBar.style.width = percentComplete + '%';
        progressPercent.innerText = percentComplete + '%';
        
        if (percentComplete === 100) {
          uploadStatusText.innerText = "Файлы успешно переданы. Обработка на сервере...";
        } else {
          uploadStatusText.innerText = `Передано: ${Math.round(e.loaded / (1024 * 1024))} МБ из ${Math.round(e.total / (1024 * 1024))} МБ`;
        }
      }
    };

    xhr.onload = function() {
      if (xhr.status === 201) {
        const response = JSON.parse(xhr.responseText);
        saveSession(response.id);
        startWaitingState(response.id);
      } else {
        alert('Ошибка при загрузке файлов. Попробуйте еще раз. Код: ' + xhr.status);
        resetToForm();
      }
    };

    xhr.onerror = function() {
      alert('Ошибка соединения с сервером.');
      resetToForm();
    };

    xhr.send(formData);
  });

  // === WAITING STATE & POLLING ===

  function startWaitingState(requestId) {
    uploadingSection.classList.add('hidden');
    waitingSection.classList.remove('hidden');
    
    requestIdDisplay.innerText = requestId;

    // Timer logic
    let seconds = 0;
    timerSeconds.innerText = seconds;
    clearInterval(timerInterval);
    timerInterval = setInterval(() => {
      seconds++;
      timerSeconds.innerText = seconds;
    }, 1000);

    // Polling logic
    clearInterval(pollInterval);
    pollInterval = setInterval(() => {
      fetch(`${API_BASE}/api/requests/${requestId}/status`)
        .then(res => {
          if (res.status === 404) {
            // Request got cleared/not found
            clearSession();
            resetToForm();
          }
          return res.json();
        })
        .then(data => {
          if (data && data.status === 'completed') {
            clearInterval(pollInterval);
            clearInterval(timerInterval);
            showResult(data);
          }
        })
        .catch(err => console.error("Ошибка при проверке статуса:", err));
    }, 3000);
  }

  function showResult(data) {
    waitingSection.classList.add('hidden');
    resultSection.classList.remove('hidden');

    const formattedMin = new Intl.NumberFormat('ru-RU').format(data.min_price);
    const formattedMax = new Intl.NumberFormat('ru-RU').format(data.max_price);

    priceRangeDisplay.innerText = `${formattedMin} - ${formattedMax} ₽`;
    specialistCommentDisplay.innerText = data.comment || "Без комментариев специалиста.";
  }

  // === RESET ===

  resetBtn.addEventListener('click', () => {
    clearSession();
    resetToForm();
  });

  function resetToForm() {
    clearInterval(pollInterval);
    clearInterval(timerInterval);
    
    // Clear State
    photoFiles = [];
    videoFile = null;
    phoneInput.value = '';
    progressBar.style.width = '0%';
    progressPercent.innerText = '0%';
    uploadStatusText.innerText = "Передача файлов на сервер оценки";
    
    renderPreviews();
    validateForm();

    resultSection.classList.add('hidden');
    waitingSection.classList.add('hidden');
    uploadingSection.classList.add('hidden');
    formSection.classList.remove('hidden');
  }

  // === SESSION STORAGE MANAGEMENT ===

  function saveSession(requestId) {
    localStorage.setItem('skolko_za_avto_id', requestId);
  }

  function clearSession() {
    localStorage.removeItem('skolko_za_avto_id');
  }

  function checkSavedSession() {
    const savedId = localStorage.getItem('skolko_za_avto_id');
    if (savedId) {
      // Restore state instantly, skip form
      formSection.classList.add('hidden');
      waitingSection.classList.remove('hidden');
      requestIdDisplay.innerText = savedId;
      
      // Fetch current database state for this request
      fetch(`${API_BASE}/api/requests/${savedId}/status`)
        .then(res => {
          if (res.status === 404) {
            clearSession();
            resetToForm();
            return null;
          }
          return res.json();
        })
        .then(data => {
          if (data) {
            if (data.status === 'completed') {
              showResult(data);
            } else {
              startWaitingState(savedId);
            }
          }
        })
        .catch(err => {
          console.error("Ошибка восстановления сессии:", err);
          // Try to poll anyway
          startWaitingState(savedId);
        });
    }
  }
});

// Register Service Worker for PWA support
if ('serviceWorker' in navigator) {
  window.addEventListener('load', () => {
    navigator.serviceWorker.register('/sw.js')
      .then(reg => console.log('Service Worker registered successfully:', reg.scope))
      .catch(err => console.error('Service Worker registration failed:', err));
  });
}

