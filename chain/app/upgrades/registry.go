package upgrades

// registered lists every upgrade this binary can run. A consensus-breaking release adds its plan
// here: one file per plan in this package exporting an Upgrade value (see the package comment),
// and its name in CURRENT. No governed upgrade has happened yet, so the list is empty; the
// networks are created fresh at their first release.
var registered = []Upgrade{}
