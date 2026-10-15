// Server-side tools the TUI has: rules feed, your ui.actions, ask the LLM, the offline write queue.
export function install(app) {
  // A laneway web for many people has none of them but the queue (hosted-routes.txt).
  for (const n of app.session && app.session.hosted ? ['offline'] : ['rules_feed', 'useractions', 'ask', 'offline']) {
    import('./' + n + '.js').then(m => m.install(app)).catch(e => console.error(n, e));
  }
}
