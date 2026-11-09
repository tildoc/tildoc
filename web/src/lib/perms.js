// Names for the permission catalogue (internal/core/permissions.go).
export const PERM_LABEL = {
  view: 'View', download: 'Download', export: 'Export', write: 'Edit', move: 'Move out', delete: 'Delete', purge: 'Delete for good',
  manage: 'Rename space', transfer: 'Transfer', drop_in: 'Drop-In token',
  'share.space.people': 'Share space with people', 'share.space.login_link': 'Space link (sign-in)', 'share.space.public_link': 'Space link (public)',
  'share.space.drop_link': 'Drop link', 'share.file.people': 'Share file with people', 'share.file.login_link': 'File link (sign-in)',
  'share.file.public_link': 'File link (public)',
  'users.manage': 'Manage people', 'teams.manage': 'Manage all teams', 'teams.create': 'Create teams', 'roles.manage': 'Manage roles',
  'spaces.create': 'Create spaces', 'spaces.recover': 'Reassign spaces', 'tags.manage': 'Manage tags', 'backups.manage': 'Backups',
  'settings.manage': 'Server settings'
};
export const PERM_HELP = {
  view: 'See the files, read them in the browser and search them. Nothing more: without Download the file itself is not handed out.',
  download: 'Get the file itself: download, WebDAV, SFTP, S3 and copying it elsewhere.',
  export: 'Export whole folders or the space as a ZIP or bundle.',
  write: 'Upload, create and edit files and folders, add new versions, tag, and move within the space.',
  move: 'Move files and folders out of this space into another one.',
  delete: 'Move files to the trash.',
  purge: 'Delete from the trash for good and empty the trash.',
  manage: "Rename the space, change its settings, and delete it once it is empty.",
  transfer: 'Hand the whole space to another owner.',
  drop_in: 'Create a Drop-In token: a key that scripts and devices use to send files into this space, and nothing else.',
  'share.space.people': 'Add people and teams to this space and set what they may do.',
  'share.space.login_link': 'Make a link to the whole space that works only for people signed in to tildoc.',
  'share.space.public_link': 'Make a link to the whole space that works for anyone who has it, without an account.',
  'share.space.drop_link': 'Make a link where anyone can upload into this space, without seeing what is in it.',
  'share.file.people': 'Share a single file with people and teams, without giving access to the rest of the space.',
  'share.file.login_link': 'Make a link to a single file that works only for people signed in to tildoc.',
  'share.file.public_link': 'Make a link to a single file that works for anyone who has it, without an account.',
  'users.manage': 'Add, change, disable and delete people on this server.',
  'teams.manage': 'Change any team, also without being a member.',
  'teams.create': 'Create new teams.',
  'roles.manage': 'Change roles and who holds server permissions.',
  'spaces.create': 'Create new spaces.',
  'spaces.recover': 'Give a space a new owner, for example when someone left. Does not allow reading it.',
  'tags.manage': 'Rename, merge and delete tags for everyone.',
  'backups.manage': 'Create and delete server backups.',
  'settings.manage': 'Change server settings, see statistics and start processing again.'
};
export const permLabel = (p) => PERM_LABEL[p] ?? p;
export const permHelp = (p) => PERM_HELP[p] ?? '';
export const canShareAny = (caps) => (caps ?? []).some((c) => c.startsWith('share.'));
// a document without a permission list (public links) decides by the link
export const canDownload = (doc) => !doc?.caps || doc.caps.includes('download');
