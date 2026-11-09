// Names for the permission catalogue (internal/core/permissions.go).
export const PERM_LABEL = {
  view: 'View', download: 'Download', export: 'Export', write: 'Edit', move: 'Move out', delete: 'Delete', purge: 'Delete for good',
  manage: 'Rename space', transfer: 'Transfer',
  'share.space.people': 'Share space with people', 'share.space.login_link': 'Space link (sign-in)', 'share.space.public_link': 'Space link (public)',
  'share.space.drop_link': 'Drop link', 'share.file.people': 'Share file with people', 'share.file.login_link': 'File link (sign-in)',
  'share.file.public_link': 'File link (public)',
  'users.manage': 'Manage people', 'teams.manage': 'Manage all teams', 'teams.create': 'Create teams', 'roles.manage': 'Manage roles',
  'spaces.create': 'Create spaces', 'spaces.recover': 'Reassign spaces', 'tags.manage': 'Manage tags', 'backups.manage': 'Backups',
  'settings.manage': 'Server settings'
};
export const permLabel = (p) => PERM_LABEL[p] ?? p;
export const canShareAny = (caps) => (caps ?? []).some((c) => c.startsWith('share.'));
// a document without a permission list (public links) decides by the link
export const canDownload = (doc) => !doc?.caps || doc.caps.includes('download');
