// Copyright 2015 Google Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package java

// This file contains the module types for compiling Java for Android, and converts the properties
// into the flags and filenames necessary to pass to the Module.  The final creation of the rules
// is handled in builder.go

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/google/blueprint"
	"github.com/google/blueprint/proptools"

	"android/soong/android"
	"android/soong/genrule"
	"android/soong/java/config"
)

func init() {
	android.RegisterModuleType("java_defaults", defaultsFactory)

	android.RegisterModuleType("java_library", LibraryFactory)
	android.RegisterModuleType("java_library_static", LibraryFactory)
	android.RegisterModuleType("java_library_host", LibraryHostFactory)
	android.RegisterModuleType("java_binary", BinaryFactory)
	android.RegisterModuleType("java_binary_host", BinaryHostFactory)
	android.RegisterModuleType("java_import", ImportFactory)
	android.RegisterModuleType("java_import_host", ImportFactoryHost)
	android.RegisterModuleType("android_app", AndroidAppFactory)

	android.RegisterSingletonType("logtags", LogtagsSingleton)
}

// TODO:
// Autogenerated files:
//  Proto
//  Renderscript
// Post-jar passes:
//  Proguard
//  Jacoco
//  Jarjar
//  Dex
// Rmtypedefs
// DroidDoc
// Findbugs

type CompilerProperties struct {
	// list of source files used to compile the Java module.  May be .java, .logtags, .proto,
	// or .aidl files.
	Srcs []string `android:"arch_variant"`

	// list of source files that should not be used to build the Java module.
	// This is most useful in the arch/multilib variants to remove non-common files
	Exclude_srcs []string `android:"arch_variant"`

	// list of directories containing Java resources
	Java_resource_dirs []string `android:"arch_variant"`

	// list of directories that should be excluded from java_resource_dirs
	Exclude_java_resource_dirs []string `android:"arch_variant"`

	// list of files to use as Java resources
	Java_resources []string `android:"arch_variant"`

	// list of files that should be excluded from java_resources
	Exclude_java_resources []string `android:"arch_variant"`

	// don't build against the default libraries (bootclasspath, legacy-test, core-junit,
	// ext, and framework for device targets)
	No_standard_libs *bool

	// don't build against the framework libraries (legacy-test, core-junit,
	// ext, and framework for device targets)
	No_framework_libs *bool

	// list of module-specific flags that will be used for javac compiles
	Javacflags []string `android:"arch_variant"`

	// list of of java libraries that will be in the classpath
	Libs []string `android:"arch_variant"`

	// list of java libraries that will be compiled into the resulting jar
	Static_libs []string `android:"arch_variant"`

	// manifest file to be included in resulting jar
	Manifest *string

	// if not blank, run jarjar using the specified rules file
	Jarjar_rules *string

	// If not blank, set the java version passed to javac as -source and -target
	Java_version *string

	// If set to false, don't allow this module to be installed.  Defaults to true.
	Installable *bool

	// If set to true, include sources used to compile the module in to the final jar
	Include_srcs *bool

	// List of modules to use as annotation processors
	Annotation_processors []string

	// List of classes to pass to javac to use as annotation processors
	Annotation_processor_classes []string
}

type CompilerDeviceProperties struct {
	// list of module-specific flags that will be used for dex compiles
	Dxflags []string `android:"arch_variant"`

	// if not blank, set to the version of the sdk to compile against
	Sdk_version string

	// Set for device java libraries, and for host versions of device java libraries
	// built for testing
	Dex bool `blueprint:"mutated"`

	// directories to pass to aidl tool
	Aidl_includes []string

	// directories that should be added as include directories
	// for any aidl sources of modules that depend on this module
	Export_aidl_include_dirs []string
}

// Module contains the properties and members used by all java module types
type Module struct {
	android.ModuleBase
	android.DefaultableModuleBase

	properties       CompilerProperties
	deviceProperties CompilerDeviceProperties

	// output file suitable for inserting into the classpath of another compile
	classpathFile android.Path

	// output file containing classes.dex
	dexJarFile android.Path

	// output files containing resources
	resourceJarFiles android.Paths

	// output file suitable for installing or running
	outputFile android.Path

	exportAidlIncludeDirs android.Paths

	logtagsSrcs android.Paths

	// filelists of extra source files that should be included in the javac command line,
	// for example R.java generated by aapt for android apps
	ExtraSrcLists android.Paths

	// installed file for binary dependency
	installFile android.Path
}

type Dependency interface {
	ClasspathFiles() android.Paths
	ResourceJarFiles() android.Paths
	AidlIncludeDirs() android.Paths
}

func InitJavaModule(module android.DefaultableModule, hod android.HostOrDeviceSupported) {
	android.InitAndroidArchModule(module, hod, android.MultilibCommon)
	android.InitDefaultableModule(module)
}

type dependencyTag struct {
	blueprint.BaseDependencyTag
	name string
}

var (
	staticLibTag     = dependencyTag{name: "staticlib"}
	libTag           = dependencyTag{name: "javalib"}
	bootClasspathTag = dependencyTag{name: "bootclasspath"}
	frameworkResTag  = dependencyTag{name: "framework-res"}
)

type sdkDep struct {
	useModule, useFiles, useDefaultLibs bool
	module                              string
	jar                                 android.Path
	aidl                                android.Path
}

func decodeSdkDep(ctx android.BaseContext, v string) sdkDep {
	switch v {
	case "", "current", "system_current", "test_current":
		// OK
	default:
		if _, err := strconv.Atoi(v); err != nil {
			ctx.PropertyErrorf("sdk_version", "invalid sdk version")
			return sdkDep{}
		}
	}

	toFile := func(v string) sdkDep {
		dir := filepath.Join("prebuilts/sdk", v)
		jar := filepath.Join(dir, "android.jar")
		aidl := filepath.Join(dir, "framework.aidl")
		jarPath := android.ExistentPathForSource(ctx, "sdkdir", jar)
		aidlPath := android.ExistentPathForSource(ctx, "sdkdir", aidl)
		if !jarPath.Valid() {
			ctx.PropertyErrorf("sdk_version", "invalid sdk version %q, %q does not exist", v, jar)
			return sdkDep{}
		}
		if !aidlPath.Valid() {
			ctx.PropertyErrorf("sdk_version", "invalid sdk version %q, %q does not exist", v, aidl)
			return sdkDep{}
		}
		return sdkDep{
			useFiles: true,
			jar:      jarPath.Path(),
			aidl:     aidlPath.Path(),
		}
	}

	toModule := func(m string) sdkDep {
		return sdkDep{
			useModule: true,
			module:    m,
		}
	}

	if ctx.AConfig().UnbundledBuild() && v != "" {
		return toFile(v)
	}

	switch v {
	case "":
		return sdkDep{
			useDefaultLibs: true,
		}
	case "current":
		return toModule("android_stubs_current")
	case "system_current":
		return toModule("android_system_stubs_current")
	case "test_current":
		return toModule("android_test_stubs_current")
	default:
		return toFile(v)
	}
}

func (j *Module) deps(ctx android.BottomUpMutatorContext) {
	if !proptools.Bool(j.properties.No_standard_libs) {
		if ctx.Device() {
			sdkDep := decodeSdkDep(ctx, j.deviceProperties.Sdk_version)
			if sdkDep.useDefaultLibs {
				ctx.AddDependency(ctx.Module(), bootClasspathTag, config.DefaultBootclasspathLibraries...)
				if !proptools.Bool(j.properties.No_framework_libs) {
					ctx.AddDependency(ctx.Module(), libTag, config.DefaultLibraries...)
				}
			}
			if sdkDep.useModule {
				ctx.AddDependency(ctx.Module(), bootClasspathTag, sdkDep.module)
			}
		} else {
			if j.deviceProperties.Dex {
				ctx.AddDependency(ctx.Module(), bootClasspathTag, config.DefaultBootclasspathLibraries...)
			}
		}
	}
	ctx.AddDependency(ctx.Module(), libTag, j.properties.Libs...)
	ctx.AddDependency(ctx.Module(), staticLibTag, j.properties.Static_libs...)
	ctx.AddDependency(ctx.Module(), libTag, j.properties.Annotation_processors...)

	android.ExtractSourcesDeps(ctx, j.properties.Srcs)
	android.ExtractSourcesDeps(ctx, j.properties.Java_resources)
}

func (j *Module) aidlFlags(ctx android.ModuleContext, aidlPreprocess android.OptionalPath,
	aidlIncludeDirs android.Paths) []string {

	localAidlIncludes := android.PathsForModuleSrc(ctx, j.deviceProperties.Aidl_includes)

	var flags []string
	if aidlPreprocess.Valid() {
		flags = append(flags, "-p"+aidlPreprocess.String())
	} else {
		flags = append(flags, android.JoinWithPrefix(aidlIncludeDirs.Strings(), "-I"))
	}

	flags = append(flags, android.JoinWithPrefix(j.exportAidlIncludeDirs.Strings(), "-I"))
	flags = append(flags, android.JoinWithPrefix(localAidlIncludes.Strings(), "-I"))
	flags = append(flags, "-I"+android.PathForModuleSrc(ctx).String())
	if src := android.ExistentPathForSource(ctx, "", "src"); src.Valid() {
		flags = append(flags, "-I"+src.String())
	}

	return flags
}

type deps struct {
	classpath          android.Paths
	bootClasspath      android.Paths
	staticJars         android.Paths
	staticJarResources android.Paths
	aidlIncludeDirs    android.Paths
	srcFileLists       android.Paths
	aidlPreprocess     android.OptionalPath
}

func (j *Module) collectDeps(ctx android.ModuleContext) deps {
	var deps deps

	sdkDep := decodeSdkDep(ctx, j.deviceProperties.Sdk_version)
	if sdkDep.useFiles {
		deps.classpath = append(deps.classpath, sdkDep.jar)
		deps.aidlIncludeDirs = append(deps.aidlIncludeDirs, sdkDep.aidl)
	}

	ctx.VisitDirectDeps(func(module blueprint.Module) {
		otherName := ctx.OtherModuleName(module)
		tag := ctx.OtherModuleDependencyTag(module)

		dep, _ := module.(Dependency)
		if dep == nil {
			switch tag {
			case android.DefaultsDepTag, android.SourceDepTag:
				// Nothing to do
			default:
				ctx.ModuleErrorf("depends on non-java module %q", otherName)
			}
			return
		}

		switch tag {
		case bootClasspathTag:
			deps.bootClasspath = append(deps.bootClasspath, dep.ClasspathFiles()...)
		case libTag:
			deps.classpath = append(deps.classpath, dep.ClasspathFiles()...)
		case staticLibTag:
			deps.classpath = append(deps.classpath, dep.ClasspathFiles()...)
			deps.staticJars = append(deps.staticJars, dep.ClasspathFiles()...)
			deps.staticJarResources = append(deps.staticJarResources, dep.ResourceJarFiles()...)
		case frameworkResTag:
			if ctx.ModuleName() == "framework" {
				// framework.jar has a one-off dependency on the R.java and Manifest.java files
				// generated by framework-res.apk
				deps.srcFileLists = append(deps.srcFileLists, module.(*AndroidApp).aaptJavaFileList)
			}
		default:
			panic(fmt.Errorf("unknown dependency %q for %q", otherName, ctx.ModuleName()))
		}

		deps.aidlIncludeDirs = append(deps.aidlIncludeDirs, dep.AidlIncludeDirs()...)
	})

	return deps
}

func (j *Module) compile(ctx android.ModuleContext) {

	j.exportAidlIncludeDirs = android.PathsForModuleSrc(ctx, j.deviceProperties.Export_aidl_include_dirs)

	deps := j.collectDeps(ctx)

	var flags javaBuilderFlags

	javacFlags := j.properties.Javacflags
	if ctx.AConfig().Getenv("EXPERIMENTAL_USE_OPENJDK9") == "" {
		javacFlags = config.StripJavac9Flags(javacFlags)
	}

	if j.properties.Java_version != nil {
		flags.javaVersion = *j.properties.Java_version
	} else {
		flags.javaVersion = "${config.DefaultJavaVersion}"
	}

	flags.bootClasspath.AddPaths(deps.bootClasspath)
	flags.classpath.AddPaths(deps.classpath)

	if len(javacFlags) > 0 {
		ctx.Variable(pctx, "javacFlags", strings.Join(javacFlags, " "))
		flags.javacFlags = "$javacFlags"
	}

	aidlFlags := j.aidlFlags(ctx, deps.aidlPreprocess, deps.aidlIncludeDirs)
	if len(aidlFlags) > 0 {
		ctx.Variable(pctx, "aidlFlags", strings.Join(aidlFlags, " "))
		flags.aidlFlags = "$aidlFlags"
	}

	srcFiles := ctx.ExpandSources(j.properties.Srcs, j.properties.Exclude_srcs)

	srcFiles = j.genSources(ctx, srcFiles, flags)

	ctx.VisitDirectDeps(func(module blueprint.Module) {
		if gen, ok := module.(genrule.SourceFileGenerator); ok {
			srcFiles = append(srcFiles, gen.GeneratedSourceFiles()...)
		}
	})

	deps.srcFileLists = append(deps.srcFileLists, j.ExtraSrcLists...)

	var jars android.Paths

	if len(srcFiles) > 0 {
		var extraJarDeps android.Paths
		if ctx.AConfig().IsEnvTrue("RUN_ERROR_PRONE") {
			// If error-prone is enabled, add an additional rule to compile the java files into
			// a separate set of classes (so that they don't overwrite the normal ones and require
			// a rebuild when error-prone is turned off).
			// TODO(ccross): Once we always compile with javac9 we may be able to conditionally
			//    enable error-prone without affecting the output class files.
			errorprone := RunErrorProne(ctx, srcFiles, deps.srcFileLists, flags, nil)
			extraJarDeps = append(extraJarDeps, errorprone)
		}

		// Compile java sources into .class files
		classes := TransformJavaToClasses(ctx, srcFiles, deps.srcFileLists, flags, extraJarDeps)
		if ctx.Failed() {
			return
		}

		jars = append(jars, classes)
	}

	dirArgs, dirDeps := ResourceDirsToJarArgs(ctx, j.properties.Java_resource_dirs, j.properties.Exclude_java_resource_dirs)
	fileArgs, fileDeps := ResourceFilesToJarArgs(ctx, j.properties.Java_resources, j.properties.Exclude_java_resources)

	var resArgs []string
	var resDeps android.Paths

	resArgs = append(resArgs, dirArgs...)
	resDeps = append(resDeps, dirDeps...)

	resArgs = append(resArgs, fileArgs...)
	resDeps = append(resDeps, fileDeps...)

	if proptools.Bool(j.properties.Include_srcs) {
		srcArgs, srcDeps := ResourceFilesToJarArgs(ctx, j.properties.Srcs, j.properties.Exclude_srcs)
		resArgs = append(resArgs, srcArgs...)
		resDeps = append(resDeps, srcDeps...)
	}

	if len(resArgs) > 0 {
		// Combine classes + resources into classes-full-debug.jar
		resourceJar := TransformResourcesToJar(ctx, resArgs, resDeps)
		if ctx.Failed() {
			return
		}

		j.resourceJarFiles = append(j.resourceJarFiles, resourceJar)
		jars = append(jars, resourceJar)
	}

	// Propagate the resources from the transitive closure of static dependencies for copying
	// into dex jars
	j.resourceJarFiles = append(j.resourceJarFiles, deps.staticJarResources...)

	// static classpath jars have the resources in them, so the resource jars aren't necessary here
	jars = append(jars, deps.staticJars...)

	manifest := android.OptionalPathForModuleSrc(ctx, j.properties.Manifest)

	// Combine the classes built from sources, any manifests, and any static libraries into
	// classes.jar.  If there is only one input jar this step will be skipped.
	outputFile := TransformJarsToJar(ctx, "classes.jar", jars, manifest, false)

	if j.properties.Jarjar_rules != nil {
		jarjar_rules := android.PathForModuleSrc(ctx, *j.properties.Jarjar_rules)
		// Transform classes.jar into classes-jarjar.jar
		outputFile = TransformJarJar(ctx, outputFile, jarjar_rules)
		if ctx.Failed() {
			return
		}
	}

	j.classpathFile = outputFile

	// TODO(ccross): handle hostdex
	if ctx.Device() && len(srcFiles) > 0 && j.installable() {
		dxFlags := j.deviceProperties.Dxflags
		if false /* emma enabled */ {
			// If you instrument class files that have local variable debug information in
			// them emma does not correctly maintain the local variable table.
			// This will cause an error when you try to convert the class files for Android.
			// The workaround here is to build different dex file here based on emma switch
			// then later copy into classes.dex. When emma is on, dx is run with --no-locals
			// option to remove local variable information
			dxFlags = append(dxFlags, "--no-locals")
		}

		if ctx.AConfig().Getenv("NO_OPTIMIZE_DX") != "" {
			dxFlags = append(dxFlags, "--no-optimize")
		}

		if ctx.AConfig().Getenv("GENERATE_DEX_DEBUG") != "" {
			dxFlags = append(dxFlags,
				"--debug",
				"--verbose",
				"--dump-to="+android.PathForModuleOut(ctx, "classes.lst").String(),
				"--dump-width=1000")
		}

		var minSdkVersion string
		switch j.deviceProperties.Sdk_version {
		case "", "current", "test_current", "system_current":
			minSdkVersion = strconv.Itoa(ctx.AConfig().DefaultAppTargetSdkInt())
		default:
			minSdkVersion = j.deviceProperties.Sdk_version
		}

		dxFlags = append(dxFlags, "--min-sdk-version="+minSdkVersion)

		flags.dxFlags = strings.Join(dxFlags, " ")

		desugarFlags := []string{
			"--min_sdk_version " + minSdkVersion,
			"--desugar_try_with_resources_if_needed=false",
			"--allow_empty_bootclasspath",
		}

		if inList("--core-library", dxFlags) {
			desugarFlags = append(desugarFlags, "--core_library")
		}

		flags.desugarFlags = strings.Join(desugarFlags, " ")

		desugarJar := TransformDesugar(ctx, outputFile, flags)
		if ctx.Failed() {
			return
		}

		// Compile classes.jar into classes.dex
		dexJarFile := TransformClassesJarToDexJar(ctx, desugarJar, flags)
		if ctx.Failed() {
			return
		}

		jars := android.Paths{dexJarFile}
		jars = append(jars, j.resourceJarFiles...)

		outputFile = TransformJarsToJar(ctx, "javalib.jar", jars, android.OptionalPath{}, true)

		j.dexJarFile = outputFile
	}
	ctx.CheckbuildFile(outputFile)
	j.outputFile = outputFile
}

func (j *Module) installable() bool {
	return j.properties.Installable == nil || *j.properties.Installable
}

var _ Dependency = (*Library)(nil)

func (j *Module) ClasspathFiles() android.Paths {
	return android.Paths{j.classpathFile}
}

func (j *Module) ResourceJarFiles() android.Paths {
	return j.resourceJarFiles
}

func (j *Module) AidlIncludeDirs() android.Paths {
	return j.exportAidlIncludeDirs
}

var _ logtagsProducer = (*Module)(nil)

func (j *Module) logtags() android.Paths {
	return j.logtagsSrcs
}

//
// Java libraries (.jar file)
//

type Library struct {
	Module
}

func (j *Library) GenerateAndroidBuildActions(ctx android.ModuleContext) {
	j.compile(ctx)

	if j.installable() {
		j.installFile = ctx.InstallFile(android.PathForModuleInstall(ctx, "framework"),
			ctx.ModuleName()+".jar", j.outputFile)
	}
}

func (j *Library) DepsMutator(ctx android.BottomUpMutatorContext) {
	j.deps(ctx)
}

func LibraryFactory() android.Module {
	module := &Library{}

	module.deviceProperties.Dex = true

	module.AddProperties(
		&module.Module.properties,
		&module.Module.deviceProperties)

	InitJavaModule(module, android.HostAndDeviceSupported)
	return module
}

func LibraryHostFactory() android.Module {
	module := &Library{}

	module.AddProperties(&module.Module.properties)

	InitJavaModule(module, android.HostSupported)
	return module
}

//
// Java Binaries (.jar file plus wrapper script)
//

type binaryProperties struct {
	// installable script to execute the resulting jar
	Wrapper string
}

type Binary struct {
	Library

	binaryProperties binaryProperties

	wrapperFile android.ModuleSrcPath
	binaryFile  android.OutputPath
}

func (j *Binary) GenerateAndroidBuildActions(ctx android.ModuleContext) {
	j.Library.GenerateAndroidBuildActions(ctx)

	// Depend on the installed jar (j.installFile) so that the wrapper doesn't get executed by
	// another build rule before the jar has been installed.
	j.wrapperFile = android.PathForModuleSrc(ctx, j.binaryProperties.Wrapper)
	j.binaryFile = ctx.InstallExecutable(android.PathForModuleInstall(ctx, "bin"),
		ctx.ModuleName(), j.wrapperFile, j.installFile)
}

func (j *Binary) DepsMutator(ctx android.BottomUpMutatorContext) {
	j.deps(ctx)
}

func BinaryFactory() android.Module {
	module := &Binary{}

	module.deviceProperties.Dex = true

	module.AddProperties(
		&module.Module.properties,
		&module.Module.deviceProperties,
		&module.binaryProperties)

	InitJavaModule(module, android.HostAndDeviceSupported)
	return module
}

func BinaryHostFactory() android.Module {
	module := &Binary{}

	module.AddProperties(
		&module.Module.properties,
		&module.Module.deviceProperties,
		&module.binaryProperties)

	InitJavaModule(module, android.HostSupported)
	return module
}

//
// Java prebuilts
//

type ImportProperties struct {
	Jars []string
}

type Import struct {
	android.ModuleBase
	prebuilt android.Prebuilt

	properties ImportProperties

	classpathFiles        android.Paths
	combinedClasspathFile android.Path
}

func (j *Import) Prebuilt() *android.Prebuilt {
	return &j.prebuilt
}

func (j *Import) PrebuiltSrcs() []string {
	return j.properties.Jars
}

func (j *Import) Name() string {
	return j.prebuilt.Name(j.ModuleBase.Name())
}

func (j *Import) DepsMutator(ctx android.BottomUpMutatorContext) {
}

func (j *Import) GenerateAndroidBuildActions(ctx android.ModuleContext) {
	j.classpathFiles = android.PathsForModuleSrc(ctx, j.properties.Jars)

	j.combinedClasspathFile = TransformJarsToJar(ctx, "classes.jar", j.classpathFiles, android.OptionalPath{}, false)
}

var _ Dependency = (*Import)(nil)

func (j *Import) ClasspathFiles() android.Paths {
	return j.classpathFiles
}

func (j *Import) ResourceJarFiles() android.Paths {
	// resources are in the ClasspathFiles
	return nil
}

func (j *Import) AidlIncludeDirs() android.Paths {
	return nil
}

var _ android.PrebuiltInterface = (*Import)(nil)

func ImportFactory() android.Module {
	module := &Import{}

	module.AddProperties(&module.properties)

	android.InitPrebuiltModule(module, &module.properties.Jars)
	android.InitAndroidArchModule(module, android.HostAndDeviceSupported, android.MultilibCommon)
	return module
}

func ImportFactoryHost() android.Module {
	module := &Import{}

	module.AddProperties(&module.properties)

	android.InitPrebuiltModule(module, &module.properties.Jars)
	android.InitAndroidArchModule(module, android.HostSupported, android.MultilibCommon)
	return module
}

func inList(s string, l []string) bool {
	for _, e := range l {
		if e == s {
			return true
		}
	}
	return false
}

//
// Defaults
//
type Defaults struct {
	android.ModuleBase
	android.DefaultsModuleBase
}

func (*Defaults) GenerateAndroidBuildActions(ctx android.ModuleContext) {
}

func (d *Defaults) DepsMutator(ctx android.BottomUpMutatorContext) {
}

func defaultsFactory() android.Module {
	return DefaultsFactory()
}

func DefaultsFactory(props ...interface{}) android.Module {
	module := &Defaults{}

	module.AddProperties(props...)
	module.AddProperties(
		&CompilerProperties{},
		&CompilerDeviceProperties{},
	)

	android.InitDefaultsModule(module)

	return module
}
