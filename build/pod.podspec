Pod::Spec.new do |spec|
  spec.name         = 'Quai'
  spec.version      = '{{.Version}}'
  spec.license      = { :type => 'GNU Lesser General Public License, Version 3.0' }
  spec.homepage     = 'https://github.com/dominant-strategies/go-quai'
  spec.authors      = { {{range .Contributors}}
		'{{.Name}}' => '{{.Email}}',{{end}}
	}
  spec.summary      = 'iOS Quai Client'
  spec.source       = { :git => 'https://github.com/dominant-strategies/go-quai.git', :commit => '{{.Commit}}' }

	spec.platform = :ios
  spec.ios.deployment_target  = '9.0'
	spec.ios.vendored_frameworks = 'Frameworks/Quai.framework'

	spec.prepare_command = <<-CMD
    curl https://quaistore.blob.core.windows.net/builds/{{.Archive}}.tar.gz | tar -xvz
    mkdir Frameworks
    mv {{.Archive}}/Quai.framework Frameworks
    rm -rf {{.Archive}}
  CMD
end
