package controllers

// MaxDisplayNameLen re-exports the display-name cap for the external
// controllers_test package, so its boundary tests move with the constant
// instead of hard-coding a number that silently rots when the cap changes.
const MaxDisplayNameLen = maxDisplayNameLen
