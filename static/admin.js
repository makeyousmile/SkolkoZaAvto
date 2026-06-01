// SkolkoZaAvto Specialist/Admin Logic

document.addEventListener('DOMContentLoaded', () => {
  // Base API configuration
  const API_BASE = window.location.origin.includes('localhost') || window.location.origin.startsWith('file://')
    ? window.location.origin
    : window.location.origin;

  // Elements
  const tabPending = document.getElementById('tab-pending');
  const tabCompleted = document.getElementById('tab-completed');
  const countPending = document.getElementById('count-pending');
  const countCompleted = document.getElementById('count-completed');
  const requestsList = document.getElementById('requests-list');

  const workspaceEmpty = document.getElementById('workspace-empty');
  const workspaceDetails = document.getElementById('workspace-details');

  const detailId = document.getElementById('detail-id');
  const detailStatus = document.getElementById('detail-status');
  const detailTime = document.getElementById('detail-time');
  const detailPhone = document.getElementById('detail-phone');
  const detailRegion = document.getElementById('detail-region');
  
  const detailVideoPlayer = document.getElementById('detail-video-player');
  const detailVideoEmpty = document.getElementById('detail-video-empty');
  const detailPhotoCount = document.getElementById('detail-photo-count');
  const detailPhotoGallery = document.getElementById('detail-photo-gallery');

  const estimateForm = document.getElementById('estimate-form');
  const estMinPrice = document.getElementById('est-min-price');
  const estMaxPrice = document.getElementById('est-max-price');
  const estComment = document.getElementById('est-comment');
  const estimateSubmitBtn = document.getElementById('estimate-submit-btn');
  const deleteRequestBtn = document.getElementById('delete-request-btn');

  // Currency Selection Elements
  const currencySelector = document.getElementById('admin-currency-selector');
  const currencyButtons = document.querySelectorAll('#admin-currency-selector .currency-btn');
  const currencySymbols = document.querySelectorAll('.currency-symbol');
  let selectedCurrency = 'BYN';

  const photoModal = document.getElementById('photo-modal');
  const modalImg = document.getElementById('modal-img');
  const modalClose = document.getElementById('modal-close');

  // Auth Elements
  const loginSection = document.getElementById('login-section');
  const adminGrid = document.getElementById('admin-grid');
  const loginForm = document.getElementById('login-form');
  const loginUsername = document.getElementById('login-username');
  const loginPassword = document.getElementById('login-password');
  const loginError = document.getElementById('login-error');
  const logoutBtn = document.getElementById('logout-btn');

  // Application State
  let requests = [];
  let selectedId = null;
  let activeTab = 'pending'; // pending / completed
  let pollInterval = null;

  // === CURRENCY SELECTION LOGIC ===
  currencyButtons.forEach(btn => {
    btn.addEventListener('click', () => {
      // If form is disabled (e.g. estimated), do not allow currency change
      if (estMinPrice.disabled) return;

      selectedCurrency = btn.getAttribute('data-currency');
      updateCurrencyUI(selectedCurrency);
    });
  });

  function updateCurrencyUI(currency) {
    currencyButtons.forEach(b => {
      if (b.getAttribute('data-currency') === currency) {
        b.classList.add('active');
      } else {
        b.classList.remove('active');
      }
    });

    // Update symbols in input labels
    currencySymbols.forEach(span => {
      span.innerText = currency;
    });

    // Update placeholders for a premium experience
    if (currency === 'BYN') {
      estMinPrice.placeholder = 'Например: 45000';
      estMaxPrice.placeholder = 'Например: 50000';
    } else {
      estMinPrice.placeholder = 'Например: 15000';
      estMaxPrice.placeholder = 'Например: 18000';
    }
  }

  // === AUTHENTICATION LOGIC ===

  function checkAuth() {
    fetch(`${API_BASE}/api/admin/requests`)
      .then(res => {
        if (res.status === 200) {
          showAdminDashboard();
        } else {
          showLoginScreen();
        }
      })
      .catch(() => {
        showLoginScreen();
      });
  }

  function showLoginScreen() {
    clearInterval(pollInterval);
    pollInterval = null;
    
    loginSection.classList.remove('hidden');
    adminGrid.classList.add('hidden');
    logoutBtn.classList.add('hidden');
  }

  function showAdminDashboard() {
    loginSection.classList.add('hidden');
    adminGrid.classList.remove('hidden');
    logoutBtn.classList.remove('hidden');
    
    fetchRequests();
    if (!pollInterval) {
      pollInterval = setInterval(fetchRequests, 5000);
    }
  }

  // Handle Login Submission
  loginForm.addEventListener('submit', (e) => {
    e.preventDefault();
    loginError.classList.add('hidden');

    const username = loginUsername.value.trim();
    const password = loginPassword.value;

    fetch(`${API_BASE}/api/admin/login`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify({ username, password })
    })
      .then(res => {
        if (res.status === 200) {
          showAdminDashboard();
        } else {
          return res.json().then(data => {
            throw new Error(data.error || "Неверный логин или пароль");
          });
        }
      })
      .catch(err => {
        loginError.innerText = err.message;
        loginError.classList.remove('hidden');
      });
  });

  // Handle Logout Click
  logoutBtn.addEventListener('click', () => {
    fetch(`${API_BASE}/api/admin/logout`, { method: 'POST' })
      .then(() => {
        showLoginScreen();
      })
      .catch(() => {
        showLoginScreen();
      });
  });

  // Check auth state on startup
  checkAuth();


  // === TABS INTERACTION ===

  tabPending.addEventListener('click', () => {
    activeTab = 'pending';
    tabPending.classList.add('active');
    tabCompleted.classList.remove('active');
    renderRequests();
  });

  tabCompleted.addEventListener('click', () => {
    activeTab = 'completed';
    tabCompleted.classList.add('active');
    tabPending.classList.remove('active');
    renderRequests();
  });

  // === DATA FETCHING & RENDERING ===

  function fetchRequests() {
    fetch(`${API_BASE}/api/admin/requests`)
      .then(res => {
        if (res.status === 401) {
          showLoginScreen();
          throw new Error("Unauthorized");
        }
        return res.json();
      })
      .then(data => {
        if (data) {
          requests = data;
          updateTabCounts();
          renderRequests();
          
          // Update details panel if a request is selected and got updated
          if (selectedId) {
            const currentReq = requests.find(r => r.id === selectedId);
            if (currentReq) {
              updateWorkspaceDetails(currentReq, false); // true update details but don't reset inputs if not changed
            }
          }
        }
      })
      .catch(err => {
        if (err.message !== "Unauthorized") {
          console.error("Ошибка при получении заявок:", err);
        }
      });
  }


  function updateTabCounts() {
    const pending = requests.filter(r => r.status === 'pending').length;
    const completed = requests.filter(r => r.status === 'completed').length;
    
    countPending.innerText = pending;
    countCompleted.innerText = completed;
  }

  function renderRequests() {
    const filtered = requests.filter(r => r.status === activeTab);
    
    if (filtered.length === 0) {
      requestsList.innerHTML = `
        <div class="empty-state" style="padding: 2rem 1rem;">
          ${activeTab === 'pending' ? 'Нет новых заявок 👍' : 'Вы еще не оценили ни одной заявки'}
        </div>
      `;
      return;
    }

    requestsList.innerHTML = '';
    
    filtered.forEach(req => {
      const card = document.createElement('div');
      card.className = `request-card ${selectedId === req.id ? 'active' : ''}`;
      
      const timeStr = formatShortDate(new Date(req.createdAt));
      const photosCount = req.photos ? req.photos.length : 0;
      const hasVideo = req.video ? '🎥 Видео' : 'Нет видео';

      let priceLine = '';
      if (activeTab === 'completed') {
        const formattedMin = new Intl.NumberFormat('ru-RU').format(req.min_price);
        const formattedMax = new Intl.NumberFormat('ru-RU').format(req.max_price);
        const curSymbol = req.currency || 'BYN';
        priceLine = `<div class="request-card-price" style="font-weight: 700; color: var(--accent-cyan); font-size: 0.85rem; margin-top: 0.35rem;">${formattedMin} - ${formattedMax} ${curSymbol}</div>`;
      }

      card.innerHTML = `
        <div class="request-card-header">
          <span class="request-card-id">ID: ${req.id}</span>
          <span class="request-card-time">${timeStr}</span>
        </div>
        <div class="request-card-phone">${req.phone}</div>
        <div class="request-card-meta">
          <span>🖼️ ${photosCount} фото</span>
          <span>${hasVideo}</span>
        </div>
        ${priceLine}
      `;

      card.addEventListener('click', () => {
        selectRequest(req.id);
      });

      requestsList.appendChild(card);
    });
  }

  function selectRequest(id) {
    selectedId = id;
    
    // Highlight in list
    const cards = requestsList.querySelectorAll('.request-card');
    cards.forEach(card => card.classList.remove('active'));
    
    // Rerender list to reflect active card
    renderRequests();

    const req = requests.find(r => r.id === id);
    if (req) {
      workspaceEmpty.classList.add('hidden');
      workspaceDetails.classList.remove('hidden');
      updateWorkspaceDetails(req, true); // force reset inputs
    }
  }

  function updateWorkspaceDetails(req, forceResetInputs) {
    detailId.innerText = `ID: ${req.id}`;
    
    // Status Badge
    if (req.status === 'pending') {
      detailStatus.className = 'status-badge pending';
      detailStatus.innerText = 'Ожидает оценки';
    } else {
      detailStatus.className = 'status-badge completed';
      detailStatus.innerText = 'Оценена';
    }

    // Created At
    const fullDateStr = new Date(req.createdAt).toLocaleString('ru-RU', {
      day: 'numeric',
      month: 'long',
      year: 'numeric',
      hour: '2-digit',
      minute: '2-digit'
    });
    detailTime.innerText = fullDateStr;

    // Region
    detailRegion.innerText = req.region || 'Не указана';

    // Client Contacts & Formatting
    detailPhone.innerText = req.phone;
    if (req.phone.startsWith('@')) {
      detailPhone.href = `https://t.me/${req.phone.substring(1)}`;
    } else {
      // Just telephone dialer
      detailPhone.href = `tel:${req.phone.replace(/[^0-9+]/g, '')}`;
    }

    // Video Player
    if (req.video) {
      detailVideoPlayer.classList.remove('hidden');
      detailVideoEmpty.classList.add('hidden');
      
      // Prevent reloading video if it's the same source to avoid breaking playback
      const videoSrc = `${API_BASE}${req.video}`;
      if (detailVideoPlayer.src !== videoSrc) {
        detailVideoPlayer.src = videoSrc;
        detailVideoPlayer.load();
      }
    } else {
      detailVideoPlayer.classList.add('hidden');
      detailVideoPlayer.src = '';
      detailVideoEmpty.classList.remove('hidden');
    }

    // Photo Gallery
    const photoCountVal = req.photos ? req.photos.length : 0;
    detailPhotoCount.innerText = photoCountVal;
    
    detailPhotoGallery.innerHTML = '';
    if (req.photos && req.photos.length > 0) {
      req.photos.forEach(photoUrl => {
        const wrapper = document.createElement('div');
        wrapper.className = 'carousel-img-wrapper';
        
        const img = document.createElement('img');
        img.src = `${API_BASE}${photoUrl}`;
        img.alt = 'Детали кузова';
        
        wrapper.appendChild(img);
        
        // Open Modal onClick
        wrapper.addEventListener('click', () => {
          openLightbox(`${API_BASE}${photoUrl}`);
        });

        detailPhotoGallery.appendChild(wrapper);
      });
    } else {
      detailPhotoGallery.innerHTML = `
        <div class="empty-state" style="grid-column: 1 / -1; padding: 1.5rem;">
          Фотографии отсутствуют
        </div>
      `;
    }

    // Form population & lock
    if (req.status === 'completed') {
      // Already Estimated
      selectedCurrency = req.currency || 'BYN';
      updateCurrencyUI(selectedCurrency);

      if (forceResetInputs) {
        estMinPrice.value = req.min_price;
        estMaxPrice.value = req.max_price;
        estComment.value = req.comment;
      }
      
      estMinPrice.disabled = true;
      estMaxPrice.disabled = true;
      estComment.disabled = true;
      
      estimateSubmitBtn.disabled = true;
      estimateSubmitBtn.innerHTML = `<span>Автомобиль успешно оценен</span> `;
      estimateSubmitBtn.style.background = 'var(--success)';
      estimateSubmitBtn.style.boxShadow = 'none';
      
      // Show delete button for completed request
      deleteRequestBtn.classList.remove('hidden');
    } else {
      // Pending valuation
      if (forceResetInputs) {
        selectedCurrency = 'BYN';
        updateCurrencyUI(selectedCurrency);
        estMinPrice.value = '';
        estMaxPrice.value = '';
        estComment.value = '';
      }
      
      estMinPrice.disabled = false;
      estMaxPrice.disabled = false;
      estComment.disabled = false;
      
      estimateSubmitBtn.disabled = false;
      estimateSubmitBtn.innerHTML = `<span>Подтвердить и отправить оценку</span> ✅`;
      estimateSubmitBtn.style.background = 'linear-gradient(135deg, #ff007f 0%, #7f00ff 100%)';
      estimateSubmitBtn.style.boxShadow = '0 0 20px rgba(255, 0, 127, 0.25)';
      
      // Hide delete button for pending request
      deleteRequestBtn.classList.add('hidden');
    }
  }

  // === FORM SUBMISSION ===

  estimateForm.addEventListener('submit', (e) => {
    e.preventDefault();
    if (!selectedId) return;

    const min = parseFloat(estMinPrice.value);
    const max = parseFloat(estMaxPrice.value);
    const comment = estComment.value.trim();

    if (min > max) {
      alert("Минимальная стоимость не может превышать максимальную!");
      return;
    }

    estimateSubmitBtn.disabled = true;
    estimateSubmitBtn.innerText = 'Отправка...';

    fetch(`${API_BASE}/api/admin/requests/${selectedId}/estimate`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify({
        min_price: min,
        max_price: max,
        currency: selectedCurrency,
        comment: comment
      })
    })
      .then(res => {
        if (res.status === 401) {
          showLoginScreen();
          throw new Error("Unauthorized");
        }
        return res.json();
      })
      .then(updatedReq => {
        if (updatedReq) {
          // Update local request array
          const idx = requests.findIndex(r => r.id === selectedId);
          if (idx !== -1) {
            requests[idx] = updatedReq;
          }
          
          updateTabCounts();
          
          // Switch to Completed tab and select the same request
          activeTab = 'completed';
          tabCompleted.classList.add('active');
          tabPending.classList.remove('active');
          
          selectRequest(selectedId);
        }
      })
      .catch(err => {
        if (err.message !== "Unauthorized") {
          console.error("Ошибка при отправке оценки:", err);
          alert("Произошла ошибка при сохранении оценки.");
          estimateSubmitBtn.disabled = false;
        }
      });

  });

  // === DELETE REQUEST ===
  deleteRequestBtn.addEventListener('click', () => {
    if (!selectedId) return;

    const currentReq = requests.find(r => r.id === selectedId);
    if (!currentReq) return;

    if (!confirm(`Вы действительно хотите безвозвратно удалить заявку ID: ${selectedId}? Все загруженные фотографии и видео этого автомобиля будут навсегда стерты с сервера.`)) {
      return;
    }

    deleteRequestBtn.disabled = true;
    deleteRequestBtn.innerText = 'Удаление...';

    fetch(`${API_BASE}/api/admin/requests/${selectedId}`, {
      method: 'DELETE'
    })
      .then(res => {
        if (res.status === 401) {
          showLoginScreen();
          throw new Error("Unauthorized");
        }
        if (res.status !== 200) {
          throw new Error("Failed to delete request");
        }
        return res.json();
      })
      .then(() => {
        // Remove from local list
        requests = requests.filter(r => r.id !== selectedId);
        
        updateTabCounts();
        renderRequests();

        // Reset workspace to empty state
        selectedId = null;
        workspaceDetails.classList.add('hidden');
        workspaceEmpty.classList.remove('hidden');

        alert("Заявка успешно удалена!");
      })
      .catch(err => {
        if (err.message !== "Unauthorized") {
          console.error("Ошибка при удалении заявки:", err);
          alert("Не удалось удалить заявку. Попробуйте еще раз.");
        }
      })
      .finally(() => {
        deleteRequestBtn.disabled = false;
        deleteRequestBtn.innerHTML = `<span>Удалить эту заявку</span> 🗑️`;
      });
  });

  // === LIGHTBOX MODAL ===

  function openLightbox(src) {
    modalImg.src = src;
    photoModal.classList.add('active');
  }

  modalClose.addEventListener('click', () => {
    photoModal.classList.remove('active');
    modalImg.src = '';
  });

  photoModal.addEventListener('click', (e) => {
    if (e.target === photoModal) {
      photoModal.classList.remove('active');
      modalImg.src = '';
    }
  });

  // === DATE UTILITIES ===

  function formatShortDate(date) {
    const now = new Date();
    const isToday = date.toDateString() === now.toDateString();
    
    const timePart = date.toLocaleTimeString('ru-RU', { hour: '2-digit', minute: '2-digit' });
    
    if (isToday) {
      return `Сегодня в ${timePart}`;
    }
    
    const yesterday = new Date(now);
    yesterday.setDate(now.getDate() - 1);
    const isYesterday = date.toDateString() === yesterday.toDateString();
    
    if (isYesterday) {
      return `Вчера в ${timePart}`;
    }
    
    return date.toLocaleDateString('ru-RU', { day: 'numeric', month: 'short' }) + `, ${timePart}`;
  }
});
