import { bindActionDelegation } from './core/actions.js';

bindActionDelegation();

// Каждая страница — отдельный эндпоинт; тип инициализации берём из data-page на <body>.
(async () => {
  const page = document.body.dataset.page || '';
  if (window.UI_MODE === 'staff') {
    const m = await import('./staff/index.js');
    if (page === 'login') await m.loginInit();
    else if (page === 'operator') await m.operatorInit();
    else if (page === 'expert') await m.expertInit();
    else if (page === 'admin') await m.adminInit();
    else if (page === 'detail') await m.detailInit();
    else if (page === 'profile') {
      const pr = await import('./staff/profile.js');
      await pr.profileInit();
    }
  } else {
    const m = await import('./applicant/index.js');
    if (page === 'new') await m.newInit();
    else if (page === 'track') await m.trackInit();
    else if (page === 'appeal') await m.appealInit();
    // intro — статичная приветственная страница, инициализация не нужна
  }
})();
