// Apply saved site colors before the first paint, without waiting for settings fetches.
(() => {
  const readColor = (name) => {
    const value = document.querySelector(`meta[name="croptop-${name}-color"]`)?.content.trim() || "";
    if (!/^#(?:[0-9a-f]{3}|[0-9a-f]{6})$/i.test(value)) return null;
    const hex = value.length === 4 ? value.slice(1).split("").map(c => c + c).join("") : value.slice(1);
    return [0, 2, 4].map(i => parseInt(hex.slice(i, i + 2), 16));
  };
  const luminance = (rgb) => rgb.map(channel => {
    const value = channel / 255;
    return value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4;
  }).reduce((sum, value, i) => sum + value * [0.2126, 0.7152, 0.0722][i], 0);
  const contrastColor = (rgb) => luminance(rgb) > 0.179 ? [0, 0, 0] : [255, 255, 255];
  const mix = (a, b, amount) => a.map((value, i) => Math.round(value * (1 - amount) + b[i] * amount));
  const css = (rgb, alpha) => alpha === undefined ? `rgb(${rgb.join(", ")})` : `rgba(${rgb.join(", ")}, ${alpha})`;

  let background = readColor("background");
  let foreground = readColor("foreground");
  if (!background && !foreground) return; // Keep the template's automatic light/dark palette.
  // If only one color was supplied, choose a readable counterpart.
  background ||= contrastColor(foreground);
  foreground ||= contrastColor(background);
  const secondaryBackground = mix(background, foreground, 0.12);
  const style = document.documentElement.style;
  const colors = {
    "--background-color": css(background),
    "--background-secondary-color": css(secondaryBackground),
    "--background-color-opacity": css(background, 0.98),
    "--background-secondary-color-opacity": css(secondaryBackground, 0.7),
    "--foreground-color": css(foreground),
    "--foreground-secondary-color": css(mix(background, foreground, 0.67)),
  };
  for (const [name, value] of Object.entries(colors)) style.setProperty(name, value);
  style.setProperty("color-scheme", luminance(background) > 0.179 ? "light" : "dark");
  for (const meta of document.querySelectorAll('meta[name="theme-color"]')) {
    meta.content = css(background);
  }
})();
