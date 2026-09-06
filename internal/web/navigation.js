// Combine a burst of navigation clicks into one absolute track selection.
class TrackNavigation {
  constructor(send, preview, setTimer = setTimeout, clearTimer = clearTimeout) {
    this.send = send;
    this.preview = preview;
    this.setTimer = setTimer;
    this.clearTimer = clearTimer;
    this.discID = '';
    this.tracks = [];
    this.current = -1;
    this.pending = null;
    this.timer = null;
  }
  update(discID, tracks, current) {
    if (discID !== this.discID || JSON.stringify(tracks) !== JSON.stringify(this.tracks)) this.cancel();
    this.discID = discID;
    this.tracks = [...tracks];
    this.current = current;
  }
  cancel() {
    if (this.timer !== null) this.clearTimer(this.timer);
    this.timer = null;
    this.pending = null;
    this.preview(null);
  }
  step(delta) {
    if (!this.discID || !this.tracks.length) return;
    const base = this.pending === null ? this.current : this.pending;
    const target = Math.max(0, Math.min(this.tracks.length - 1, base + delta));
    if (this.timer !== null) this.clearTimer(this.timer);
    if (target === this.current) { this.cancel(); return; }
    this.pending = target;
    this.preview(this.tracks[target]);
    this.timer = this.setTimer(() => {
      const track = this.tracks[this.pending], discID = this.discID;
      this.timer = null;
      this.pending = null;
      this.preview(null);
      this.send(track, discID);
    }, 250);
  }
}
if (typeof module !== 'undefined') module.exports = TrackNavigation;
