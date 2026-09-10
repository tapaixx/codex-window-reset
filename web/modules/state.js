export function createStore(initialState = {}) {
  let state = structuredClone(initialState);
  const listeners = new Set();
  return {
    getState: () => state,
    subscribe(listener) {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    dispatch(action) {
      state = reduceState(state, action);
      listeners.forEach((listener) => listener(state, action));
      return state;
    },
  };
}

export function reduceState(state, action) {
  switch (action.type) {
    case 'status-loaded': return { ...state, status: action.value, loading: false };
    case 'accounts-loaded': return { ...state, accounts: action.value };
    case 'schedule-loaded': return { ...state, schedule: action.value, draft: structuredClone(action.value) };
    case 'draft-changed': return { ...state, draft: { ...state.draft, ...action.patch } };
    case 'history-loaded': return { ...state, history: action.value };
    case 'feedback': return { ...state, feedback: action.value };
    default: return state;
  }
}
